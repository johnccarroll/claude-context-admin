package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const transcript = `{"type":"user","message":{"content":"hi"},"timestamp":"2026-09-01T00:00:00Z"}
{"type":"assistant","timestamp":"2026-09-02T00:00:00Z","message":{"content":[{"type":"tool_use","name":"mcp__supabase__execute_sql","input":{}},{"type":"tool_use","name":"Skill","input":{"skill":"superpowers:brainstorming"}}]}}
{"type":"assistant","timestamp":"2026-09-03T00:00:00Z","message":{"content":[{"type":"tool_use","name":"mcp__supabase__list_tables","input":{}},{"type":"tool_use","name":"Read","input":{"file_path":"/h/.claude/projects/p/memory/a.md"}},{"type":"tool_use","name":"Read","input":{"file_path":"/h/src/x.go"}},{"type":"tool_use","name":"Agent","input":{"subagent_type":"Explore"}}]}}
not json "tool_use"
`

func TestScanCountsAndCaches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proj", "s.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache.json")
	u := Scan(dir, time.Time{}, cache)
	want := map[string]Stat{
		"mcp:supabase":                           {2, "2026-09-03T00:00:00Z"},
		"skill:superpowers:brainstorming":        {1, "2026-09-02T00:00:00Z"},
		"file:/h/.claude/projects/p/memory/a.md": {1, "2026-09-03T00:00:00Z"},
		"agent:Explore":                          {1, "2026-09-03T00:00:00Z"},
	}
	if len(u) != len(want) {
		t.Fatalf("got %v", u)
	}
	for k, v := range want {
		if u[k] != v {
			t.Errorf("%s = %+v, want %+v", k, u[k], v)
		}
	}
	// Second scan must come from the cache: make the file unreadable but keep size+mtime.
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p, 0o644)
	u2 := Scan(dir, time.Time{}, cache)
	if u2["mcp:supabase"].Count != 2 {
		t.Fatalf("cache not used: %v", u2)
	}
	// Files older than the window are skipped.
	if u3 := Scan(dir, time.Now().Add(time.Hour), ""); len(u3) != 0 {
		t.Fatalf("window ignored: %v", u3)
	}
}
