package loads

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

const repo = "/r"

func inv() *scan.Inventory {
	return &scan.Inventory{Entries: []entry.Entry{
		{Kind: entry.Instructions, Scope: entry.ScopeUser, Path: "/h/.claude/CLAUDE.md", Bytes: 400, Meta: map[string]any{"imports": []string{"/h/shared.md"}}},
		{Kind: entry.Instructions, Scope: entry.ScopeImport, Path: "/h/shared.md", Bytes: 400, Meta: map[string]any{"importedBy": "/h/.claude/CLAUDE.md"}},
		{Kind: entry.Rule, Scope: entry.ScopeUser, Path: "/h/.claude/rules/always.md", Bytes: 40},
		{Kind: entry.Rule, Scope: entry.ScopeUser, Path: "/h/.claude/rules/go.md", Bytes: 4000, Meta: map[string]any{"paths": []any{"*.go"}}},
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: repo, Path: "/r/AGENTS.md", Bytes: 800},
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: repo, Path: "/r/web/AGENTS.md", Bytes: 9000},
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: "/other", Path: "/other/AGENTS.md", Bytes: 9000},
		{Kind: entry.MemoryIndex, Scope: entry.ScopeProject, Project: repo, Path: "/m/MEMORY.md", Bytes: 40000, Lines: 400},
		{Kind: entry.Plugin, Name: "on", Enabled: true, Meta: map[string]any{"alwaysOnTokens": 1000}},
		{Kind: entry.Plugin, Name: "off", Enabled: false, Meta: map[string]any{"alwaysOnTokens": 5000}},
		{Kind: entry.MCP, Scope: entry.ScopeUser, Name: "db", Enabled: true},
		{Kind: entry.MCP, Scope: entry.ScopeLocal, Project: repo, Name: "db", Enabled: true},
		{Kind: entry.MCP, Scope: entry.ScopeProject, Project: "/other", Name: "db", Enabled: true},
		{Kind: entry.Skill, Scope: entry.ScopeUser, Name: "deploy"},
		{Kind: entry.Skill, Scope: entry.ScopeProject, Project: repo, Name: "deploy"},
		{Kind: entry.Skill, Scope: entry.ScopePlugin, Name: "deploy"},
	}}
}

func TestComputeBudget(t *testing.T) {
	b := Compute(inv(), repo)
	got := map[string]int{}
	for _, s := range b.Sources {
		got[s.Label] = s.Tokens
	}
	// user CLAUDE.md + import + path-less rule; the paths-scoped rule loads on demand
	if got["Your instructions"] != (400+400+40)/4 {
		t.Errorf("user instructions = %d", got["Your instructions"])
	}
	// root AGENTS.md only (no CLAUDE.md); web/AGENTS.md and other repos are excluded
	if got["Project instructions"] != 800/4 {
		t.Errorf("project instructions = %d", got["Project instructions"])
	}
	// MEMORY.md is cut at 200 lines (half of 40000 bytes) and then at 25 KB
	if got["Memory index"] != 20000/4 || b.IndexLines != 400 {
		t.Errorf("memory index = %d lines=%d", got["Memory index"], b.IndexLines)
	}
	if got["Plugins"] != 1000 {
		t.Errorf("plugins = %d", got["Plugins"])
	}
	if b.Total != 210+200+5000+1000+got["Your skills, commands and agents"] {
		t.Errorf("total = %d", b.Total)
	}
}

func TestShadowsFollowPrecedence(t *testing.T) {
	sh := Shadows(inv(), repo)
	if len(sh) != 2 {
		t.Fatalf("shadows = %+v", sh)
	}
	for _, s := range sh {
		switch s.Kind {
		case entry.MCP: // local beats user; the other repo's server doesn't apply here
			if s.Winner.Scope != entry.ScopeLocal || len(s.Hidden) != 1 {
				t.Errorf("mcp: %+v", s)
			}
		case entry.Skill: // personal beats project; plugin skills are namespaced
			if s.Winner.Scope != entry.ScopeUser || len(s.Hidden) != 1 {
				t.Errorf("skill: %+v", s)
			}
		}
	}
}

func TestNestedProjectReadsAncestorInstructions(t *testing.T) {
	b := Compute(inv(), "/r/web")
	for _, s := range b.Sources {
		if s.Label == "Project instructions" && s.Tokens != (800+9000)/4 {
			t.Errorf("a session in /r/web reads /r/AGENTS.md and /r/web/AGENTS.md; got %d tokens", s.Tokens)
		}
	}
}

func TestInstructionModes(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "r")
	in := &scan.Inventory{Home: home, Entries: []entry.Entry{
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: repo, Path: filepath.Join(repo, "AGENTS.md"), Bytes: 400},
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: repo, Path: filepath.Join(repo, "CLAUDE.md"), Bytes: 800},
	}}
	proj := func() int {
		for _, s := range Compute(in, repo).Sources {
			if s.Label == "Project instructions" {
				return s.Tokens
			}
		}
		return -1
	}
	if got := proj(); got != 800/4 {
		t.Errorf("default with a CLAUDE.md: AGENTS.md is ignored; got %d", got)
	}
	set := func(json string) {
		_ = os.MkdirAll(filepath.Join(repo, ".claude"), 0o755)
		_ = os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(json), 0o644)
	}
	set(`{"instructionFiles":"claude-md-and-agents-md"}`)
	if got := proj(); got != 1200/4 {
		t.Errorf("both: %d", got)
	}
	set(`{"projectInstructions":"none"}`) // the older key is still honoured
	if got := proj(); got != 0 {
		t.Errorf("managed-only via legacy key: %d", got)
	}
	in.Entries = in.Entries[:1] // no CLAUDE.md: default falls back to AGENTS.md
	set(`{}`)
	if got := proj(); got != 400/4 {
		t.Errorf("fallback: %d", got)
	}
}

func TestImportOfListedFileCountsOnce(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "r")
	agents := filepath.Join(repo, "AGENTS.md")
	in := &scan.Inventory{Home: home, Entries: []entry.Entry{
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: repo, Path: agents, Bytes: 4000},
		{Kind: entry.Instructions, Scope: entry.ScopeProject, Project: repo, Path: filepath.Join(repo, "CLAUDE.md"), Bytes: 20,
			Meta: map[string]any{"imports": []string{agents}}},
	}}
	for _, s := range Compute(in, repo).Sources {
		if s.Label == "Project instructions" && s.Tokens != 4020/4 {
			t.Errorf("CLAUDE.md importing AGENTS.md should count both once: %d", s.Tokens)
		}
	}
}
