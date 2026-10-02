package scan

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

// fixtureHome copies testdata/home into a temp dir, writes real absolute paths where the
// fixture says HOME, names project dirs the way Claude Code encodes them, and symlinks the
// global memory dir into a "vault" (memory folders can be symlinks into a notes app).
func fixtureHome(t *testing.T) string {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	src := filepath.Join("testdata", "home")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = strings.Replace(rel, "-H-repo", Encode(filepath.Join(home, "repo")), 1)
		rel = strings.Replace(rel, string(filepath.Separator)+"-H", string(filepath.Separator)+Encode(home), 1)
		dst := filepath.Join(home, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, []byte(strings.ReplaceAll(string(b), "HOME", home)), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(home, ".claude", "projects", Encode(filepath.Join(home, "repo")), "a.jsonl")
	_ = os.Chtimes(old, timeZero, timeZero) // a.jsonl (real path) is older than b.jsonl (renamed path)
	if err := os.Symlink(filepath.Join(home, "vault", "mem"), filepath.Join(home, ".claude", "projects", Encode(home), "memory")); err != nil {
		t.Fatal(err)
	}
	return home
}

var timeZero = time.Unix(1_600_000_000, 0)

func fakeCLI(home string) Runner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "plugin list --json":
			// Pretty-printed across lines, with a notice first, like the real CLI can emit.
			return []byte(`[
  {"id":"p1@mk","version":"1.0.0","scope":"user","enabled":true,"installPath":"` + filepath.Join(home, "plugins", "p1") + `"},
  {"id":"p1@other","version":"2","scope":"user","enabled":false,"installPath":""}
]`), nil
		case "plugin details p1@mk":
			return []byte("p1 1.0.0\n  Description: Says hello.\n\nComponent inventory\n  Skills (1)  hello\n  Hooks (1)\n\nProjected token cost\n  Always-on:   ~1,288 tok   added to every session\n\nPer-component (rounded)\n  component  always-on  on-invoke\n  hello           ~140      ~2.2k\n"), nil
		}
		return nil, errors.New("unexpected: " + strings.Join(args, " "))
	}
}

func scanFixture(t *testing.T) (*Inventory, string) {
	home := fixtureHome(t)
	inv := (&Scanner{Home: home, Run: fakeCLI(home), Costs: true}).Scan(context.Background())
	return inv, home
}

func find(inv *Inventory, kind entry.Kind, name string) *entry.Entry {
	for i := range inv.Entries {
		if inv.Entries[i].Kind == kind && inv.Entries[i].Name == name {
			return &inv.Entries[i]
		}
	}
	return nil
}

func has(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestProjectsResolveRenamedRepoAndGlobal(t *testing.T) {
	inv, home := scanFixture(t)
	var gotRepo, gotGlobal bool
	for _, p := range inv.Projects {
		if p.Path == filepath.Join(home, "repo") && p.Exists {
			gotRepo = true
		}
		if p.Global {
			gotGlobal = true
		}
	}
	if !gotRepo || !gotGlobal {
		t.Fatalf("projects = %+v", inv.Projects)
	}
}

func TestMemories(t *testing.T) {
	inv, _ := scanFixture(t)
	k := find(inv, entry.Memory, "keychain")
	if k == nil || k.Scope != entry.ScopeUser || k.Type != "reference" {
		t.Fatalf("symlinked global memory not read: %+v", k)
	}
	p := find(inv, entry.Memory, "parity")
	if p == nil || p.Type != "project" || len(p.Issues) != 0 {
		t.Fatalf("a top-level type is read and is not a problem: %+v", p)
	}
	if !has(p.Links, "parity") || !has(p.Links, "missing-note") {
		t.Fatalf("links not normalised: %v", p.Links)
	}
	bad := find(inv, entry.Memory, "x")
	if bad == nil || !has(bad.Issues, "bad-frontmatter") || bad.Description == "" {
		t.Fatalf("bad yaml should still yield fields: %+v", bad)
	}
	var idx *entry.Entry
	for i := range inv.Entries {
		e := &inv.Entries[i]
		if e.Kind == entry.MemoryIndex && e.Scope == entry.ScopeProject {
			idx = e
		}
		if strings.HasSuffix(e.Path, ".consolidate-lock") {
			t.Fatal("non-markdown file scanned")
		}
	}
	if idx == nil || !has(idx.Issues, "index-near-cap") {
		t.Fatalf("161-line index should be near cap: %+v", idx)
	}
}

func TestInstructionsAndImports(t *testing.T) {
	inv, home := scanFixture(t)
	g := find(inv, entry.Instructions, "CLAUDE.md")
	if g == nil || g.Scope != entry.ScopeUser {
		t.Fatalf("global CLAUDE.md: %+v", g)
	}
	if !has(g.Issues, "import-missing:./nope.md") || has(g.Links, "~/in-fence.md") {
		t.Fatalf("imports: links=%v issues=%v", g.Links, g.Issues)
	}
	s := find(inv, entry.Instructions, "shared.md")
	if s == nil || s.Scope != entry.ScopeImport || s.Meta["importedBy"] != filepath.Join(home, ".claude", "CLAUDE.md") {
		t.Fatalf("import entry: %+v", s)
	}
	if find(inv, entry.Instructions, "AGENTS.md") == nil {
		t.Fatal("repo AGENTS.md missing")
	}
	r := find(inv, entry.Rule, "go.md")
	if r == nil || r.Meta["paths"] == nil {
		t.Fatalf("rule paths: %+v", r)
	}
}

func TestToolkit(t *testing.T) {
	inv, _ := scanFixture(t)
	if s := find(inv, entry.Skill, "deploy"); s == nil || s.Description != "Deploy the app to prod." || s.Scope != entry.ScopeProject {
		t.Fatalf("folded description: %+v", s)
	}
	if c := find(inv, entry.Command, "ops:apply"); c == nil {
		t.Fatal("nested command name")
	}
	if a := find(inv, entry.Agent, "obsidian"); a == nil || a.Scope != entry.ScopeUser {
		t.Fatalf("agent: %+v", a)
	}
}

func TestMCPNeverKeepsSecrets(t *testing.T) {
	inv, _ := scanFixture(t)
	b, _ := json.Marshal(inv)
	if strings.Contains(string(b), "SECRETVALUE123") {
		t.Fatal("a secret value leaked into the inventory")
	}
	supa := find(inv, entry.MCP, "supa")
	if supa == nil || !has(supa.Meta["envKeys"].([]string), "SUPABASE_ACCESS_TOKEN") {
		t.Fatalf("env key names should survive: %+v", supa)
	}
	if l := find(inv, entry.MCP, "linear"); l == nil || l.Meta["url"] != "https://mcp.linear.app/mcp" || l.Meta["transport"] != "http" {
		t.Fatalf("url query should be stripped: %+v", l)
	}
	if loc := find(inv, entry.MCP, "loc"); loc == nil || loc.Scope != entry.ScopeLocal {
		t.Fatalf("local scope: %+v", loc)
	}
	if db := find(inv, entry.MCP, "db"); db == nil || db.Scope != entry.ScopeProject || db.Enabled {
		t.Fatalf("project server disabled for this project: %+v", db)
	}
}

func TestHooks(t *testing.T) {
	inv, _ := scanFixture(t)
	stop := find(inv, entry.Hook, "Stop")
	if stop == nil || stop.Scope != entry.ScopeLocal || len(stop.Issues) != 1 || !strings.HasPrefix(stop.Issues[0], "hook-script-missing:") {
		t.Fatalf("dead hook script: %+v", stop)
	}
	pre := find(inv, entry.Hook, "PreToolUse")
	if pre == nil || strings.Contains(pre.Meta["command"].(string), "SECRET") || pre.Meta["matcher"] != "Bash" {
		t.Fatalf("hook command must be redacted: %+v", pre)
	}
}

func TestPlugins(t *testing.T) {
	inv, _ := scanFixture(t)
	var p1 []*entry.Entry
	for i := range inv.Entries {
		if e := &inv.Entries[i]; e.Kind == entry.Plugin && e.Name == "p1" {
			p1 = append(p1, e)
		}
	}
	if len(p1) != 2 || !has(p1[0].Issues, "plugin-installed-twice") || !has(p1[1].Issues, "plugin-installed-twice") {
		t.Fatalf("duplicate plugin: %+v", p1)
	}
	main := p1[0]
	if main.Meta["id"] != "p1@mk" {
		main = p1[1]
	}
	if main.Meta["alwaysOnTokens"] != 1288 || main.Description != "Says hello." {
		t.Fatalf("details: %+v", main.Meta)
	}
	if h := find(inv, entry.Skill, "hello"); h == nil || h.Scope != entry.ScopePlugin || h.Meta["plugin"] != "p1" {
		t.Fatalf("plugin skill: %+v", h)
	}
	if h := find(inv, entry.Hook, "SessionStart"); h == nil || h.Scope != entry.ScopePlugin {
		t.Fatalf("plugin hook: %+v", h)
	}
}

func TestTokensAndRedact(t *testing.T) {
	for in, want := range map[string]int{"~1,288": 1288, "2.2k": 2200, "140": 140} {
		if got := tokens(in); got != want {
			t.Errorf("tokens(%q) = %d", in, got)
		}
	}
	if got := redact("x --token=abc Bearer zzz"); strings.Contains(got, "abc") || strings.Contains(got, "zzz") {
		t.Errorf("redact: %s", got)
	}
}

func TestImportsAcceptBareFileNames(t *testing.T) {
	got := imports("# x\n@AGENTS.md\n@~/a b.md\n@someone said hi\n@john\n")
	if len(got) != 2 || got[0] != "AGENTS.md" || got[1] != "~/a b.md" {
		t.Fatalf("imports: %q", got)
	}
}

func TestMemoryTitleComesFromIndex(t *testing.T) {
	home := t.TempDir()
	d := filepath.Join(home, ".claude", "projects", Encode(home), "memory")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "feedback_small_prs.md"), []byte("---\nname: small-prs\n---\nx\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "MEMORY.md"), []byte("- [Small pull requests](feedback_small_prs.md) — keep PRs small\n"), 0o644)
	inv := (&Scanner{Home: home, Run: fakeCLI(home)}).Scan(context.Background())
	for _, e := range inv.Entries {
		if e.Kind == entry.Memory && e.Meta["title"] != "Small pull requests" {
			t.Fatalf("title %v", e.Meta["title"])
		}
	}
}

func TestHostileImportsAreNeverRead(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	_ = os.MkdirAll(repo, 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"x":{"env":{"API_KEY":"sk-SECRET-123"}}}}`), 0o600)
	_ = os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("@/dev/zero\n@~/.claude.json\n@notes.md\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "notes.md"), []byte("fine\n"), 0o644)
	_ = os.Symlink("/dev/zero", filepath.Join(repo, "AGENTS.md"))
	pd := filepath.Join(home, ".claude", "projects", Encode(repo))
	_ = os.MkdirAll(pd, 0o755)
	_ = os.WriteFile(filepath.Join(pd, "s.jsonl"), []byte(`{"cwd":"`+repo+`"}`+"\n"), 0o644)
	done := make(chan *Inventory)
	go func() { done <- (&Scanner{Home: home}).Scan(context.Background()) }()
	var inv *Inventory
	select {
	case inv = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scan hung on /dev/zero")
	}
	for _, e := range inv.Entries {
		readable := e.Kind == entry.Instructions || e.Kind == entry.Rule // what the app and MCP serve
		if readable && (strings.HasSuffix(e.Path, ".claude.json") || e.Path == "/dev/zero" || strings.HasSuffix(e.Path, "AGENTS.md")) {
			t.Errorf("listed (and so readable): %s", e.Path)
		}
	}
	if find(inv, entry.Instructions, "notes.md") == nil {
		t.Error("a normal Markdown import should still be listed")
	}
}

func TestSnippetIsRuneSafe(t *testing.T) {
	text := strings.Repeat("Ⱥ", 300) + "zzq"
	if got := Snippet(text, "zzq"); !strings.HasSuffix(got, "zzq") {
		t.Fatalf("snippet: %q", got)
	}
	if Snippet("short", "nope") != "short" {
		t.Fatal("no match should give the start")
	}
}

func TestRedactPathTokens(t *testing.T) {
	for in, want := range map[string]string{
		"https://mcp.example.com/s/a1B2c3D4e5F6g7H8i9J0k/mcp": "https://mcp.example.com/s/••••/mcp",
		"https://mcp.linear.app/mcp":                          "https://mcp.linear.app/mcp",
		"https://api.example.com/v2/servers/streamable-http":  "https://api.example.com/v2/servers/streamable-http",
	} {
		if got := redactPath(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
