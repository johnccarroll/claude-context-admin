package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

func TestQuietCaps(t *testing.T) {
	src := "---\nname: NEVER\n---\n- NEVER run X without --dry-run.\n- ❌ **NEVER** skip tests\nYou MUST keep `MUST_STAY` and IMPORTANT: check.\n```\nNEVER touch code\n```\n"
	got, n := QuietCaps(src)
	want := "---\nname: NEVER\n---\n- Never run X without --dry-run.\n- ❌ **Never** skip tests\nYou must keep `MUST_STAY` and important: check.\n```\nNEVER touch code\n```\n"
	if got != want || n != 4 {
		t.Fatalf("n=%d\n%s", n, got)
	}
}

func TestHealthFindings(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "AGENTS.md")
	body := strings.Repeat("You MUST do this.\n", 5) + "Think step by step.\nUse claude-3-opus for this.\nSee ~/nowhere/script.sh and `~/in/code`.\n"
	_ = os.WriteFile(f, []byte(body), 0o644)
	got := map[string]Finding{}
	for _, x := range health(dir, []entry.Entry{{Kind: entry.Instructions, Scope: entry.ScopeProject, Path: f}}) {
		got[x.Code] = x
	}
	for _, c := range []string{"pressure-language", "dated-scaffold", "retired-model", "missing-path"} {
		if _, ok := got[c]; !ok {
			t.Errorf("missing %s in %v", c, got)
		}
	}
	if p := got["missing-path"]; len(p.Evidence) != 1 || p.Evidence[0].Text != "~/nowhere/script.sh" {
		t.Errorf("paths inside inline code must be ignored: %+v", p.Evidence)
	}
	if got["pressure-language"].Evidence[0].Line != 1 {
		t.Errorf("line numbers: %+v", got["pressure-language"].Evidence)
	}
	// plugin files are never flagged
	if len(health(dir, []entry.Entry{{Kind: entry.Skill, Scope: entry.ScopePlugin, Path: f}})) != 0 {
		t.Error("plugin skills must be skipped")
	}
}

func TestMemoryWriteProblems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := filepath.Join(home, ".claude", "projects", "-p", "memory")
	_ = os.MkdirAll(dir, 0o755)
	// A repo folder that happens to be called memory is the repo's business, not a memory.
	docs := filepath.Join(home, "repo", "docs", "memory", "notes.md")
	_ = os.MkdirAll(filepath.Dir(docs), 0o755)
	_ = os.WriteFile(docs, []byte("no header\n"), 0o644)
	if p := MemoryWriteProblems(docs); p != nil {
		t.Fatalf("a repo's docs/memory file was treated as a memory: %v", p)
	}
	good := filepath.Join(dir, "a.md")
	bad := filepath.Join(dir, "b.md")
	_ = os.WriteFile(good, []byte("---\nname: a\n---\nx\n"), 0o644)
	_ = os.WriteFile(bad, []byte("---\ndescription: \"x\" y\n---\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [A](a.md) — x\n"), 0o644)
	if p := MemoryWriteProblems(good); len(p) != 0 {
		t.Fatalf("good memory: %v", p)
	}
	if p := MemoryWriteProblems(bad); len(p) != 2 {
		t.Fatalf("bad header and missing index line: %v", p)
	}
	if p := MemoryWriteProblems("/tmp/notes/x.md"); p != nil {
		t.Fatal("non-memory files are ignored")
	}
}
