package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedSettingsAndConfigDir(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	managed := filepath.Join(t.TempDir(), "managed-settings.json")
	_ = os.WriteFile(managed, []byte(`{"instructionFiles":"claude-md"}`), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(`{"instructionFiles":"claude-md-and-agents-md"}`), 0o644)
	old := ManagedSettings
	ManagedSettings = managed
	defer func() { ManagedSettings = old }()
	if m := InstructionMode(home, repo); m != ModeClaudeOnly {
		t.Fatalf("the organization's setting must win over the project's: %s", m)
	}

	// CLAUDE_CONFIG_DIR moves the user folder, for the real home only.
	real, _ := os.UserHomeDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere")
	if ConfigDir(real) != "/elsewhere" || GlobalConfig(real) != "/elsewhere/.claude.json" {
		t.Fatalf("config dir: %s %s", ConfigDir(real), GlobalConfig(real))
	}
	if ConfigDir(home) != filepath.Join(home, ".claude") || GlobalConfig(home) != filepath.Join(home, ".claude.json") {
		t.Fatal("a --home sandbox must keep its own folder")
	}
}

func TestPluginListIsReusedUntilPluginStateChanges(t *testing.T) {
	home := t.TempDir()
	reg := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	_ = os.MkdirAll(filepath.Dir(reg), 0o755)
	_ = os.WriteFile(reg, []byte(`{}`), 0o644)
	calls := 0
	s := &Scanner{Home: home, Plugins: &PluginCache{}, Run: func(_ context.Context, a ...string) ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}}
	for range 3 {
		s.Scan(t.Context())
	}
	if calls != 1 {
		t.Fatalf("claude plugin list ran %d times with nothing changed", calls)
	}
	_ = os.WriteFile(reg, []byte(`{"plugins":{}}`), 0o644)
	s.Scan(t.Context())
	if calls != 2 {
		t.Fatalf("a plugin change must re-run it: %d", calls)
	}
}

func TestHostileRepoFilesAreSkippedQuickly(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "projects", "-repo"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "projects", "-repo", "s.jsonl"), []byte(`{"cwd":"`+repo+`"}`+"\n"), 0o644)
	_ = os.Symlink("/dev/zero", filepath.Join(repo, ".mcp.json"))
	_ = os.MkdirAll(filepath.Join(repo, ".claude", "commands"), 0o755)
	_ = os.Symlink("/dev/zero", filepath.Join(repo, ".claude", "settings.json"))
	secret := filepath.Join(home, "creds")
	_ = os.WriteFile(secret, []byte("aws_secret=XYZ\n"), 0o600)
	_ = os.Symlink(secret, filepath.Join(repo, ".claude", "commands", "x.md"))
	done := make(chan *Inventory, 1)
	go func() { done <- (&Scanner{Home: home}).Scan(t.Context()) }()
	select {
	case inv := <-done:
		for _, e := range inv.Entries {
			if strings.HasSuffix(e.Path, "x.md") {
				t.Fatal("a .md link to a non-Markdown file was listed")
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a repo's /dev/zero links hung the scan")
	}
}

func TestMCPStampIgnoresEverythingButServers(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".claude.json")
	_ = os.WriteFile(cfg, []byte(`{"numStartups":1,"mcpServers":{"a":{"command":"x"}}}`), 0o644)
	a := MCPStamp(home)
	_ = os.WriteFile(cfg, []byte(`{"numStartups":2,"tipsHistory":{"x":1},"mcpServers":{"a":{"command":"x"}}}`), 0o644)
	if MCPStamp(home) != a {
		t.Fatal("a stats-only rewrite changed the stamp")
	}
	_ = os.WriteFile(cfg, []byte(`{"mcpServers":{"a":{"command":"y"}}}`), 0o644)
	if MCPStamp(home) == a {
		t.Fatal("a server change didn't change the stamp")
	}
}

func TestURLCredentialsAreStrippedFirst(t *testing.T) {
	for _, u := range []string{"https://alice:hun#ter2@mcp.example.com/mcp", "https://bob:pa?ss9@mcp.example.com/mcp", " https://c:hunter2@mcp.example.com/x?k=1"} {
		if got := mcpMeta(mcpServer{URL: u})["url"].(string); strings.Contains(got, "hun") || strings.Contains(got, "pa") || strings.Contains(got, "hunter2") {
			t.Fatalf("%q -> %q", u, got)
		}
	}
}
