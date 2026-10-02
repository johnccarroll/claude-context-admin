// Package loads works out, for one project, what Claude Code reads before the first message,
// what it costs, and which definition wins when two scopes define the same name.
package loads

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// Source is one slice of the start-of-session budget.
type Source struct {
	Label  string   `json:"label"`
	Detail string   `json:"detail"`
	Tokens int      `json:"tokens"` // estimate; 0 means loaded on demand
	Paths  []string `json:"paths,omitempty"`
	View   string   `json:"view,omitempty"` // the page that manages this source, when it isn't files
}

// Budget is everything loaded at session start for one project.
type Budget struct {
	Project    string   `json:"project"`
	Sources    []Source `json:"sources"`
	Total      int      `json:"total"`
	IndexLines int      `json:"indexLines"` // MEMORY.md lines, against scan.IndexMaxLines
	IndexBytes int64    `json:"indexBytes"`
	Mode       string   `json:"mode"` // instructionFiles setting in effect
}

// Shadow is a name defined in more than one scope; Winner is what Claude Code uses.
type Shadow struct {
	Kind   entry.Kind    `json:"kind"`
	Name   string        `json:"name"`
	Winner entry.Entry   `json:"winner"`
	Hidden []entry.Entry `json:"hidden"`
}

// Estimate converts bytes of prose to tokens. Good enough to rank sources; not billing-grade.
func Estimate(bytes int64) int { return int(bytes / 4) }

// listingTokens approximates one skill/agent/command line in the session's tool listing.
func listingTokens(e entry.Entry) int { return (len(e.Name)+len(e.Description))/4 + 8 }

// applies reports whether a user/project/local/plugin entry is in effect for project.
func applies(e entry.Entry, project string) bool {
	switch e.Scope {
	case entry.ScopeUser, entry.ScopePlugin:
		return true
	case entry.ScopeProject, entry.ScopeLocal:
		return e.Project == project
	}
	return false
}

// Compute builds the budget for project (an absolute repo path).
func Compute(inv *scan.Inventory, project string) Budget {
	b := Budget{Project: project}
	byPath := map[string]entry.Entry{}
	for _, e := range inv.Entries {
		if e.Kind == entry.Instructions || e.Kind == entry.Rule {
			byPath[e.Path] = e
		}
	}
	// Each file counts once, even when imported from several places.
	counted := map[string]bool{}
	withImports := func(roots []entry.Entry) (int64, []string) {
		var size int64
		var paths []string
		var walk func(e entry.Entry)
		walk = func(e entry.Entry) {
			if counted[e.Path] {
				return
			}
			counted[e.Path] = true
			size += e.Bytes
			paths = append(paths, e.Path)
			imps, _ := e.Meta["imports"].([]string)
			for _, p := range imps {
				if c, ok := byPath[p]; ok {
					walk(c)
				}
			}
		}
		for _, r := range roots {
			walk(r)
		}
		return size, paths
	}

	// Claude Code reads instruction files in the launch folder and every folder above it. Which of
	// CLAUDE.md / AGENTS.md count depends on the instructionFiles setting; under the default, a
	// project with no CLAUDE.md of its own gets its AGENTS.md files instead.
	mode := scan.InstructionMode(inv.Home, project)
	var userIns, projIns, agents []entry.Entry
	hasClaude := false
	for _, e := range inv.Entries {
		switch {
		case e.Kind == entry.Instructions && e.Scope == entry.ScopeUser:
			userIns = append(userIns, e)
		case e.Kind == entry.Rule && e.Meta["paths"] == nil && applies(e, project):
			// rules without a paths: filter load every session
			if e.Scope == entry.ScopeUser {
				userIns = append(userIns, e)
			} else {
				projIns = append(projIns, e)
			}
		case e.Kind == entry.Instructions && e.Scope != entry.ScopeImport && e.Scope != entry.ScopeUser:
			dir := ownerDir(e.Path)
			if filepath.Base(e.Path) == "CLAUDE.md" && (e.Project == project || within(project, dir)) {
				hasClaude = true
			}
			if !within(project, dir) {
				continue
			}
			if filepath.Base(e.Path) == "AGENTS.md" {
				agents = append(agents, e)
			} else {
				projIns = append(projIns, e)
			}
		}
	}
	switch {
	case mode == scan.ModeManagedOnly:
		userIns, projIns = nil, nil
	case mode == scan.ModeBoth, mode == scan.ModeAgentsFallback && !hasClaude:
		projIns = append(projIns, agents...)
	}
	b.Mode = mode
	us, up := withImports(userIns)
	ps, pp := withImports(projIns)
	b.Sources = append(b.Sources,
		Source{Label: "Your instructions", Detail: "CLAUDE.md, rules and imports", Tokens: Estimate(us), Paths: up},
		Source{Label: "Project instructions", Detail: "CLAUDE.md / AGENTS.md and imports", Tokens: Estimate(ps), Paths: pp})

	for _, e := range inv.Entries {
		if e.Kind == entry.MemoryIndex && e.Project == project && e.Meta["agent"] == nil {
			b.IndexLines, b.IndexBytes = e.Lines, e.Bytes
			load := e.Bytes
			if e.Lines > scan.IndexMaxLines {
				load = e.Bytes * scan.IndexMaxLines / int64(e.Lines)
			}
			if load > scan.IndexMaxBytes {
				load = scan.IndexMaxBytes
			}
			b.Sources = append(b.Sources, Source{Label: "Memory index", Detail: "MEMORY.md", Tokens: Estimate(load), Paths: []string{e.Path}})
		}
	}

	plug, nPlug := 0, 0
	listing, nSkills, nMCP := 0, 0, 0
	for _, e := range inv.Entries {
		switch {
		case e.Kind == entry.Plugin && e.Enabled:
			nPlug++
			if t, ok := e.Meta["alwaysOnTokens"].(int); ok {
				plug += t
			}
		case (e.Kind == entry.Skill || e.Kind == entry.Command || e.Kind == entry.Agent) && e.Scope != entry.ScopePlugin && applies(e, project):
			nSkills++
			listing += listingTokens(e)
		case e.Kind == entry.MCP && e.Enabled && applies(e, project):
			nMCP++
		}
	}
	b.Sources = append(b.Sources,
		Source{Label: "Plugins", Detail: strconv.Itoa(nPlug) + " enabled", Tokens: plug, View: "plugins"},
		Source{Label: "Your skills, commands and agents", Detail: strconv.Itoa(nSkills) + " listed", Tokens: listing, View: "skills"},
		Source{Label: "MCP servers", Detail: strconv.Itoa(nMCP) + " servers, tools load on demand", View: "mcp"})
	for _, s := range b.Sources {
		b.Total += s.Tokens
	}
	return b
}

// MCP precedence: local > project > user > plugin.
var mcpRank = map[entry.Scope]int{entry.ScopeLocal: 0, entry.ScopeProject: 1, entry.ScopeUser: 2, entry.ScopePlugin: 3}

// Skills, commands and agents: personal (user) beats project. Plugin ones are namespaced and never collide.
var skillRank = map[entry.Scope]int{entry.ScopeUser: 0, entry.ScopeProject: 1}

// Shadows lists names defined in more than one scope for project.
func Shadows(inv *scan.Inventory, project string) []Shadow {
	groups := map[string][]entry.Entry{}
	for _, e := range inv.Entries {
		if !applies(e, project) {
			continue
		}
		switch e.Kind {
		case entry.MCP:
			groups[string(e.Kind)+"\x00"+e.Name] = append(groups[string(e.Kind)+"\x00"+e.Name], e)
		case entry.Skill, entry.Command, entry.Agent:
			if e.Scope != entry.ScopePlugin {
				groups[string(e.Kind)+"\x00"+e.Name] = append(groups[string(e.Kind)+"\x00"+e.Name], e)
			}
		}
	}
	out := []Shadow{} // never null in JSON
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		rank := skillRank
		if g[0].Kind == entry.MCP {
			rank = mcpRank
		}
		sort.SliceStable(g, func(i, j int) bool { return rank[g[i].Scope] < rank[g[j].Scope] })
		out = append(out, Shadow{Kind: g[0].Kind, Name: g[0].Name, Winner: g[0], Hidden: g[1:]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ownerDir is the folder an instruction file belongs to (.claude/CLAUDE.md belongs to its parent).
func ownerDir(path string) string {
	d := filepath.Dir(path)
	if filepath.Base(d) == ".claude" {
		return filepath.Dir(d)
	}
	return d
}

// within reports whether dir is project or one of its ancestors. Files in subfolders load only
// when Claude works there, so they're not part of the start-of-session budget.
func within(project, dir string) bool {
	return project == dir || strings.HasPrefix(project, dir+string(filepath.Separator))
}
