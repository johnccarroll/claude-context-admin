package scan

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

// Claude Code loads only this much of MEMORY.md at session start.
const (
	IndexMaxLines = 200
	IndexMaxBytes = 25 * 1024
)

func (s *Scanner) memories(inv *Inventory) {
	for _, p := range inv.Projects {
		dir := filepath.Join(s.claudeDir(), "projects", p.Dir, "memory")
		owner := p.Path
		switch {
		case p.Global:
			owner = ""
		case owner == "": // no transcript left to say where it lived; keep it apart from global memory
			owner = p.Dir
		}
		scope := entry.ScopeProject
		if p.Global {
			scope = entry.ScopeUser
		}
		s.memoryDir(inv, dir, owner, scope, nil)
	}
	// Subagent memory: ~/.claude/agent-memory/<agent>/ (user) and <repo>/.claude/agent-memory*/<agent>/.
	agentDirs := func(root string) []string {
		des, _ := os.ReadDir(root)
		var out []string
		for _, de := range des {
			out = append(out, filepath.Join(root, de.Name()))
		}
		return out
	}
	for _, d := range agentDirs(filepath.Join(s.claudeDir(), "agent-memory")) {
		s.memoryDir(inv, d, "", entry.ScopeUser, map[string]any{"agent": filepath.Base(d)})
	}
	for _, r := range inv.repos() {
		for _, sub := range []string{"agent-memory", "agent-memory-local"} {
			sc := entry.ScopeProject
			if sub == "agent-memory-local" {
				sc = entry.ScopeLocal
			}
			for _, d := range agentDirs(filepath.Join(r.Path, ".claude", sub)) {
				s.memoryDir(inv, d, r.Path, sc, map[string]any{"agent": filepath.Base(d)})
			}
		}
	}
}

// indexTitle matches a MEMORY.md line's "[Title](file.md)".
var indexTitle = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+\.md)\)`)

func (s *Scanner) memoryDir(inv *Inventory, dir, project string, scope entry.Scope, meta map[string]any) {
	des, err := os.ReadDir(dir) // follows a symlinked dir
	if err != nil {
		return
	}
	titles := map[string]string{} // file -> the human title its MEMORY.md line gives it
	if idx, err := ReadText(filepath.Join(dir, "MEMORY.md")); err == nil {
		for _, m := range indexTitle.FindAllStringSubmatch(string(idx), -1) {
			titles[m[2]] = m[1]
		}
	}
	for _, de := range des {
		name := de.Name()
		if !strings.HasSuffix(name, ".md") {
			continue // e.g. .consolidate-lock
		}
		path := filepath.Join(dir, name)
		src, fi, ok := readFile(path)
		if !ok {
			continue
		}
		d := parseDoc(src)
		e := entry.Entry{Scope: scope, Project: project, Path: path, Enabled: true,
			Bytes: fi.Size(), Lines: d.lines, Modified: fi.ModTime().UTC().Format(time.RFC3339), Meta: maps.Clone(meta)}
		if name == "MEMORY.md" {
			e.Kind = entry.MemoryIndex
			e.Name = "MEMORY.md"
			if d.lines > IndexMaxLines || fi.Size() > IndexMaxBytes {
				e.Issues = append(e.Issues, "index-over-cap")
			} else if d.lines > IndexMaxLines*3/4 {
				e.Issues = append(e.Issues, "index-near-cap")
			}
			inv.Entries = append(inv.Entries, e)
			continue
		}
		e.Kind = entry.Memory
		e.Name = d.fm.Name
		if e.Name == "" {
			e.Name = strings.TrimSuffix(name, ".md")
		}
		e.Description = d.fm.Description
		e.Type = d.memType()
		e.Links = wikilinks(d.body)
		e.Meta = setMeta(e.Meta, "stem", strings.TrimSuffix(name, ".md"))
		if t := titles[name]; t != "" {
			e.Meta = setMeta(e.Meta, "title", t)
		}
		switch {
		case !d.hasFM:
			e.Issues = append(e.Issues, "no-frontmatter")
		case d.badYAML:
			e.Issues = append(e.Issues, "bad-frontmatter")
		}
		inv.Entries = append(inv.Entries, e)
	}
}

func setMeta(m map[string]any, k string, v any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	m[k] = v
	return m
}

func metaStrings(v any) []string { s, _ := v.([]string); return s }
