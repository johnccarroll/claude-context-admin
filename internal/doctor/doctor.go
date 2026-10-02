// Package doctor checks every place cca depends on Claude Code internals against the live
// machine, so a Claude Code update that changes a format is noticed the day it ships.
// Each check maps to one row in docs/COMPAT.md.
package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/usage"
)

// Status of one check.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn" // works, but something looks off
	Fail Status = "fail" // a format we rely on has changed; that feature is degraded
)

// Check is one dependency on Claude Code.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // what degrades, and where to look
}

// Run performs every check. run calls the claude CLI (nil skips CLI checks).
func Run(ctx context.Context, home string, run scan.Runner) []Check {
	var out []Check
	add := func(c Check) { out = append(out, c) }

	if run != nil {
		v, err := run(ctx, "--version")
		if err != nil {
			add(Check{"Claude Code CLI", Fail, "`claude --version` failed: " + err.Error(), "Plugin and MCP changes are unavailable until `claude` is on PATH."})
		} else {
			add(Check{"Claude Code CLI", OK, strings.TrimSpace(string(v)), ""})
		}
	}

	inv := (&scan.Scanner{Home: home, Run: run, Costs: run != nil}).Scan(ctx)
	for _, w := range inv.Warnings {
		add(Check{"Scanner warning", Warn, w, "See docs/COMPAT.md for the file or command named."})
	}
	count := func(k entry.Kind) (n int, issues map[string]int) {
		issues = map[string]int{}
		for _, e := range inv.Entries {
			if e.Kind == k {
				n++
				for _, i := range e.Issues {
					c, _, _ := strings.Cut(i, ":")
					issues[c]++
				}
			}
		}
		return
	}

	// Project folder names → real paths. Only folders with transcripts can be judged: empty ones
	// (left behind by deleted worktrees) have nothing to resolve from.
	resolved, total := 0, 0
	for _, p := range inv.Projects {
		if t, _ := filepath.Glob(filepath.Join(scan.ConfigDir(home), "projects", p.Dir, "*.jsonl")); len(t) == 0 {
			continue
		}
		total++
		if p.Path != "" {
			resolved++
		}
	}
	switch {
	case total == 0:
		add(Check{"Project folders", Warn, "no project transcripts found", ""})
	case resolved*10 < total*8:
		add(Check{"Project folders", Fail, fmt.Sprintf("only %d of %d project folders with transcripts resolved to a path", resolved, total),
			"Transcripts may no longer record `cwd`, or the folder naming changed. Check scan.resolveDir."})
	default:
		add(Check{"Project folders", OK, fmt.Sprintf("%d of %d resolved", resolved, total), ""})
	}

	// Memory header format.
	n, issues := count(entry.Memory)
	bad := issues["bad-frontmatter"] + issues["no-frontmatter"]
	switch {
	case n == 0:
		add(Check{"Memory files", Warn, "no memories found", ""})
	case bad*5 > n:
		add(Check{"Memory files", Fail, fmt.Sprintf("%d of %d memory headers couldn't be read", bad, n),
			"Claude Code may have changed its memory header format. Check scan.parseDoc and write.SetFrontmatter."})
	default:
		add(Check{"Memory files", OK, fmt.Sprintf("%d memories, %d with unreadable headers", n, bad), ""})
	}

	// Plugins via the CLI.
	if run != nil {
		np, _ := count(entry.Plugin)
		priced := 0
		for _, e := range inv.Entries {
			if e.Kind == entry.Plugin && e.Enabled && e.Meta["alwaysOnTokens"] != nil {
				priced++
			}
		}
		switch {
		case np == 0:
			if out, err := run(ctx, "plugin", "list", "--json"); err == nil && strings.TrimSpace(string(out)) == "[]" {
				add(Check{"Plugins", OK, "no plugins installed", ""})
			} else {
				add(Check{"Plugin list", Warn, "`claude plugin list --json` returned no plugins cca could read", "If you have plugins, its JSON shape changed; check scan.plugins."})
			}
		case priced == 0:
			add(Check{"Plugin costs", Fail, "no token costs could be read from `claude plugin details`",
				"Its text output changed (it has no --json). Plugin costs show as unknown; check scan.parseDetails."})
		default:
			add(Check{"Plugins", OK, fmt.Sprintf("%d plugins, costs read for %d enabled", np, priced), ""})
		}
	}

	// Transcripts → usage stats.
	add(transcripts(filepath.Join(scan.ConfigDir(home), "projects")))

	return out
}

// transcripts compares raw tool calls in recent transcripts with what the usage parser understood.
func transcripts(projects string) Check {
	files, _ := filepath.Glob(filepath.Join(projects, "*", "*.jsonl"))
	sort.Slice(files, func(i, j int) bool { return modTime(files[i]).After(modTime(files[j])) })
	if len(files) > 20 {
		files = files[:20]
	}
	raw := 0
	for _, f := range files {
		raw += countToolUse(f)
	}
	if raw == 0 {
		return Check{"Usage stats", Warn, "no tool calls in recent transcripts to check against", ""}
	}
	u := usage.Scan(projects, time.Now().Add(-30*24*time.Hour), "")
	understood := 0
	for _, s := range u {
		understood += s.Count
	}
	if understood == 0 {
		return Check{"Usage stats", Fail, fmt.Sprintf("%d tool calls in recent transcripts, none understood", raw),
			"The transcript format changed. Usage numbers show as unknown until usage.parseFile is updated."}
	}
	return Check{"Usage stats", OK, fmt.Sprintf("transcripts readable (%d MCP, skill, agent and memory uses in 30 days)", understood), ""}
}

func countToolUse(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if bytes.Contains(sc.Bytes(), []byte(`"tool_use"`)) {
			if json.Valid(sc.Bytes()) {
				n++
			}
		}
	}
	return n
}

func modTime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// Worst returns the most severe status.
func Worst(cs []Check) Status {
	w := OK
	for _, c := range cs {
		if c.Status == Fail {
			return Fail
		}
		if c.Status == Warn {
			w = Warn
		}
	}
	return w
}
