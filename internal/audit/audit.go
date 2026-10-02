// Package audit turns an inventory into a summary and findings.
package audit

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/usage"
)

// Thresholds for usage-based findings.
const (
	StaleAfter   = 90 * 24 * time.Hour // a memory older than this and never opened is a prune candidate
	CostlyPlugin = 300                 // always-on tokens worth flagging when a plugin goes unused
)

// Finding is one problem, located precisely enough to fix.
type Finding struct {
	Code       string     `json:"code"`
	Path       string     `json:"path"`
	Project    string     `json:"project,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	Confidence string     `json:"confidence,omitempty"` // instruction-health findings: High, Medium or Low
	Evidence   []Evidence `json:"evidence,omitempty"`   // the lines it points at
	Candidates []string   `json:"candidates,omitempty"` // folder-moved: where the folder may have gone
}

// Report is what `cca audit --json` prints.
type Report struct {
	Counts      map[string]int `json:"counts"`
	BrokenLinks int            `json:"brokenLinks"`
	Findings    []Finding      `json:"findings"`
	Warnings    []string       `json:"warnings,omitempty"`
}

// Run builds the report. u may be nil when transcripts weren't scanned; usage findings are then skipped.
// Memory [[links]] resolve the way the UI will: by file stem, then by `name:`, in the same memory
// directory first, then the global one.
func Run(inv *scan.Inventory, u usage.Usage, now time.Time) Report {
	r := Report{Counts: map[string]int{}, Findings: []Finding{}, Warnings: inv.Warnings}
	for _, e := range inv.Entries {
		r.Counts[string(e.Kind)]++
		for _, code := range e.Issues {
			c, detail, _ := strings.Cut(code, ":")
			r.Findings = append(r.Findings, Finding{Code: c, Path: e.Path, Project: e.Project, Detail: detail})
		}
	}
	for _, f := range brokenLinks(inv) {
		r.BrokenLinks++
		r.Findings = append(r.Findings, f)
	}
	r.Findings = append(r.Findings, copies(inv)...)
	r.Findings = append(r.Findings, agentsMD(inv)...)
	r.Findings = append(r.Findings, movedFolders(inv)...)
	r.Findings = append(r.Findings, health(inv.Home, inv.Entries)...)
	if u != nil {
		r.Findings = append(r.Findings, unused(inv, u, now)...)
	}
	sort.SliceStable(r.Findings, func(i, j int) bool { return r.Findings[i].Code < r.Findings[j].Code })
	return r
}

func brokenLinks(inv *scan.Inventory) []Finding {
	byDir := map[string]map[string]bool{} // memory dir -> known stems and names
	globalDir := ""
	for _, e := range inv.Entries {
		if e.Kind != entry.Memory {
			continue
		}
		d := filepath.Dir(e.Path)
		if byDir[d] == nil {
			byDir[d] = map[string]bool{}
		}
		if stem, ok := e.Meta["stem"].(string); ok {
			byDir[d][stem] = true
		}
		byDir[d][e.Name] = true
		if e.Scope == entry.ScopeUser && e.Meta["agent"] == nil {
			globalDir = d
		}
	}
	var out []Finding
	for _, e := range inv.Entries {
		if e.Kind != entry.Memory {
			continue
		}
		d := filepath.Dir(e.Path)
		for _, l := range e.Links {
			if byDir[d][l] || (globalDir != "" && byDir[globalDir][l]) {
				continue
			}
			out = append(out, Finding{Code: "broken-link", Path: e.Path, Project: e.Project, Detail: l})
		}
	}
	return out
}

// copies finds skills, commands and agents whose identical file is committed to several projects;
// one user-scope copy would serve all of them.
func copies(inv *scan.Inventory) []Finding {
	groups := map[string][]entry.Entry{}
	for _, e := range inv.Entries {
		if e.Scope != entry.ScopeProject || (e.Kind != entry.Skill && e.Kind != entry.Command && e.Kind != entry.Agent) {
			continue
		}
		b, err := scan.ReadText(e.Path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		k := string(e.Kind) + "\x00" + e.Name + "\x00" + string(sum[:])
		groups[k] = append(groups[k], e)
	}
	var out []Finding
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		var projects []string
		for _, e := range g {
			projects = append(projects, filepath.Base(e.Project))
		}
		sort.Strings(projects)
		out = append(out, Finding{Code: "copied-across-projects", Path: g[0].Path,
			Detail: string(g[0].Kind) + " " + g[0].Name + " in " + strings.Join(projects, ", ")})
	}
	return out
}

// unused flags things that cost context but that no transcript in the window shows Claude using.
func unused(inv *scan.Inventory, u usage.Usage, now time.Time) []Finding {
	pluginSkills := map[string][]string{} // plugin -> skill names it ships
	for _, e := range inv.Entries {
		if e.Scope == entry.ScopePlugin && e.Kind == entry.Skill {
			if p, ok := e.Meta["plugin"].(string); ok {
				pluginSkills[p] = append(pluginSkills[p], e.Name)
			}
		}
	}
	used := func(prefix string) bool {
		for k, v := range u {
			if v.Count > 0 && strings.HasPrefix(k, prefix) {
				return true
			}
		}
		return false
	}
	var out []Finding
	for _, e := range inv.Entries {
		switch e.Kind {
		case entry.Plugin:
			cost, _ := e.Meta["alwaysOnTokens"].(int)
			if !e.Enabled || cost < CostlyPlugin {
				continue
			}
			hit := used("skill:"+e.Name+":") || used("mcp:plugin_"+e.Name+"_")
			for _, s := range pluginSkills[e.Name] {
				hit = hit || u["skill:"+s].Count > 0
			}
			if !hit {
				out = append(out, Finding{Code: "plugin-unused", Path: e.Path, Detail: e.Name})
			}
		case entry.MCP:
			if e.Enabled && u["mcp:"+e.Name].Count == 0 {
				out = append(out, Finding{Code: "mcp-unused", Path: e.Path, Project: e.Project, Detail: e.Name})
			}
		case entry.Memory:
			m, err := time.Parse(time.RFC3339, e.Modified)
			if err == nil && now.Sub(m) > StaleAfter && u["file:"+e.Path].Count == 0 {
				out = append(out, Finding{Code: "memory-never-recalled", Path: e.Path, Project: e.Project})
			}
		}
	}
	return out
}

// agentsMD flags AGENTS.md files Claude Code ignores (the project also has a CLAUDE.md, under the
// default setting) and project CLAUDE.md files that could become AGENTS.md, which other agent
// tools read too (Claude Code 2.1.277+ reads AGENTS.md natively).
func agentsMD(inv *scan.Inventory) []Finding {
	type files struct{ claude, agents []string }
	byProject := map[string]*files{}
	imported := map[string]bool{}
	for _, e := range inv.Entries {
		if imps, ok := e.Meta["imports"].([]string); ok {
			for _, p := range imps {
				imported[p] = true
			}
		}
	}
	for _, e := range inv.Entries {
		if e.Kind != entry.Instructions || e.Scope != entry.ScopeProject || e.Project == "" {
			continue
		}
		f := byProject[e.Project]
		if f == nil {
			f = &files{}
			byProject[e.Project] = f
		}
		switch filepath.Base(e.Path) {
		case "CLAUDE.md":
			f.claude = append(f.claude, e.Path)
		case "AGENTS.md":
			f.agents = append(f.agents, e.Path)
		}
	}
	var out []Finding
	for project, f := range byProject {
		mode := scan.InstructionMode(inv.Home, project)
		if mode == scan.ModeAgentsFallback && len(f.claude) > 0 {
			for _, a := range f.agents {
				if !imported[a] { // CLAUDE.md containing @AGENTS.md loads it anyway
					out = append(out, Finding{Code: "agents-md-ignored", Path: a, Project: project})
				}
			}
		}
		if mode != scan.ModeClaudeOnly && mode != scan.ModeManagedOnly {
			for _, c := range f.claude {
				if filepath.Dir(c) == project && !exists(filepath.Join(project, "AGENTS.md")) {
					out = append(out, Finding{Code: "claude-md-convertible", Path: c, Project: project})
				}
			}
		}
	}
	return out
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
