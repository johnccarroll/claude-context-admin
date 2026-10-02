package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnccarroll/claude-context-admin/internal/proposal"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

func connect(t *testing.T, home string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := New(home, "test", nil).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

func TestToolsReadAndOnlyPropose(t *testing.T) {
	home := t.TempDir()
	mem := filepath.Join(home, ".claude", "projects", "-p", "memory")
	_ = os.MkdirAll(mem, 0o755)
	a := filepath.Join(mem, "reference_keychain.md")
	_ = os.WriteFile(a, []byte("---\nname: keychain\ndescription: where secrets live\n---\nUse security find-generic-password.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"db":{"command":"x","env":{"TOKEN":"SECRET123"}}}}`), 0o644)
	cs := connect(t, home)

	var hits []map[string]any
	call(t, cs, "memory_search", map[string]any{"query": "generic-password"}, &hits)
	if len(hits) != 1 || hits[0]["path"] != a {
		t.Fatalf("search by full text: %+v", hits)
	}
	var read map[string]any
	call(t, cs, "memory_read", map[string]any{"path": a}, &read)
	if !strings.Contains(read["content"].(string), "find-generic-password") {
		t.Fatalf("read: %+v", read)
	}
	if res := call(t, cs, "memory_read", map[string]any{"path": filepath.Join(home, ".claude.json")}, nil); !res.IsError {
		t.Fatal("config files must not be readable")
	}
	var items []map[string]any
	call(t, cs, "toolkit_list", map[string]any{}, &items)
	b, _ := json.Marshal(items)
	if strings.Contains(string(b), "SECRET123") || !strings.Contains(string(b), "TOKEN") {
		t.Fatalf("toolkit must show key names, never values: %s", b)
	}
	if res := call(t, cs, "propose_change", map[string]any{"op": "rm -rf", "args": map[string]any{}, "reason": "x"}, nil); !res.IsError {
		t.Fatal("unknown ops must be refused")
	}
	call(t, cs, "propose_change", map[string]any{"op": "memory-trash", "args": map[string]any{"path": a}, "reason": "Superseded by the keychain catalog."}, nil)
	ps := proposal.Load(write.DataDir(home))
	if len(ps) != 1 || ps[0].Op != "memory-trash" || ps[0].Status != proposal.Pending {
		t.Fatalf("proposal: %+v", ps)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("proposing must not change anything")
	}
}

// What reaches Claude through the tools never carries credentials or files dressed up as Markdown.
func TestToolsHideCredentialsAndLinkedFiles(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	_ = os.MkdirAll(filepath.Join(home, ".claude", "projects", "-repo"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "projects", "-repo", "s.jsonl"), []byte(`{"cwd":"`+repo+`"}`+"\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	secret := filepath.Join(home, "creds")
	_ = os.WriteFile(secret, []byte("aws_secret=XYZ\n"), 0o600)
	_ = os.Symlink(secret, filepath.Join(repo, "notes.md"))
	_ = os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("@notes.md\n"), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"api":{"type":"http","url":"https://alice:hunter2@mcp.example.com/mcp"}}}`), 0o644)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"curl -u admin:hunter2pass https://x"}]}]}}`), 0o644)
	cs := connect(t, home)

	var items []map[string]any
	call(t, cs, "toolkit_list", map[string]any{}, &items)
	var budget map[string]any
	call(t, cs, "context_budget", map[string]any{"project": repo}, &budget)
	b, _ := json.Marshal([]any{items, budget})
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("a credential reached Claude: %s", b)
	}
	res := call(t, cs, "memory_read", map[string]any{"path": filepath.Join(repo, "notes.md")}, nil)
	if !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		t.Fatalf("a symlink named .md served a non-Markdown file: %s", b)
	}
}
