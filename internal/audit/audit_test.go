package audit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/usage"
)

func mem(path, name string, scope entry.Scope, links ...string) entry.Entry {
	stem := path[len(path)-len(name)-3 : len(path)-3]
	return entry.Entry{Kind: entry.Memory, Scope: scope, Path: path, Name: name, Links: links,
		Meta: map[string]any{"stem": stem}}
}

func TestBrokenLinksResolveByStemNameAndGlobal(t *testing.T) {
	inv := &scan.Inventory{Entries: []entry.Entry{
		mem("/g/keychain.md", "keychain", entry.ScopeUser),
		mem("/p/parity.md", "parity", entry.ScopeProject, "parity", "keychain", "nope"),
		{Kind: entry.Hook, Path: "/s.json", Issues: []string{"hook-script-missing:~/x.sh"}},
	}}
	r := Run(inv, nil, time.Now())
	if r.BrokenLinks != 1 {
		t.Fatalf("broken = %d, findings %+v", r.BrokenLinks, r.Findings)
	}
	var sawHook bool
	for _, f := range r.Findings {
		if f.Code == "hook-script-missing" && f.Detail == "~/x.sh" {
			sawHook = true
		}
	}
	if !sawHook || r.Counts["memory"] != 2 {
		t.Fatalf("report: %+v", r)
	}
}

func TestCopiesAndUnused(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	same := "---\nname: explore\n---\nsame body\n"
	old := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(time.RFC3339)
	inv := &scan.Inventory{Entries: []entry.Entry{
		{Kind: entry.Skill, Scope: entry.ScopeProject, Project: "/a", Name: "explore", Path: write("a/SKILL.md", same)},
		{Kind: entry.Skill, Scope: entry.ScopeProject, Project: "/b", Name: "explore", Path: write("b/SKILL.md", same)},
		{Kind: entry.Skill, Scope: entry.ScopeProject, Project: "/c", Name: "explore", Path: write("c/SKILL.md", "different")},
		{Kind: entry.Plugin, Name: "apps", Enabled: true, Meta: map[string]any{"alwaysOnTokens": 627}},
		{Kind: entry.Plugin, Name: "vercel", Enabled: true, Meta: map[string]any{"alwaysOnTokens": 4219}},
		{Kind: entry.Skill, Scope: entry.ScopePlugin, Name: "deploy", Meta: map[string]any{"plugin": "vercel"}},
		{Kind: entry.MCP, Name: "supabase", Enabled: true},
		{Kind: entry.MCP, Name: "weather", Enabled: true},
		{Kind: entry.Memory, Name: "stale", Path: "/m/stale.md", Modified: old},
		{Kind: entry.Memory, Name: "read", Path: "/m/read.md", Modified: old},
	}}
	u := usage.Usage{"skill:deploy": {Count: 3}, "mcp:supabase": {Count: 9}, "file:/m/read.md": {Count: 1}}
	got := map[string]string{}
	for _, f := range Run(inv, u, time.Now()).Findings {
		got[f.Code+"/"+f.Detail+f.Path] = f.Code
	}
	for _, want := range []string{"copied-across-projects/skill explore in a, b" + filepath.Join(dir, "a/SKILL.md"),
		"plugin-unused/apps", "mcp-unused/weather", "memory-never-recalled//m/stale.md"} {
		if got[want] == "" {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("unexpected findings: %v", got)
	}
}

func TestPrivateFoldersAreNotProbed(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{"/h/Downloads/recovery-codes.txt", true},
		{"/h/Desktop", true},
		{"/h/Library/Mobile Documents/iCloud~md~obsidian/x", true},
		{"/Volumes/Archive/x", true},
		{"/h/dev/app/README.md", false},
		{"/h/DesktopStuff/x", false},
	} {
		if Private(c.path, "/h") != c.want {
			t.Errorf("%s: want %v", c.path, c.want)
		}
	}
}
