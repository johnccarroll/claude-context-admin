package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func home(t *testing.T, transcript string) string {
	h := t.TempDir()
	d := filepath.Join(h, ".claude", "projects", "-p")
	_ = os.MkdirAll(filepath.Join(d, "memory"), 0o755)
	_ = os.WriteFile(filepath.Join(d, "memory", "a.md"), []byte("---\nname: a\n---\nx\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "s.jsonl"), []byte(transcript), 0o644)
	return h
}

func find(cs []Check, name string) Check {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestUsageCheckDetectsFormatChange(t *testing.T) {
	ok := `{"cwd":"/x","timestamp":"2026-09-30T00:00:00Z","message":{"content":[{"type":"tool_use","name":"mcp__db__q","input":{}}]}}` + "\n"
	if c := find(Run(context.Background(), home(t, ok), nil), "Usage stats"); c.Status != OK {
		t.Fatalf("known format: %+v", c)
	}
	// A future format that moves tool calls somewhere the parser doesn't look.
	changed := `{"cwd":"/x","timestamp":"2026-09-30T00:00:00Z","event":{"type":"tool_use","tool":"mcp__db__q"}}` + "\n"
	cs := Run(context.Background(), home(t, changed), nil)
	if c := find(cs, "Usage stats"); c.Status != Fail || Worst(cs) != Fail {
		t.Fatalf("changed format must fail loudly: %+v", c)
	}
}
