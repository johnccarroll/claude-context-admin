package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

// Claude Code follows @imports at most this many levels deep.
const MaxImportDepth = 4

var skipDirs = map[string]bool{"node_modules": true, ".git": true, "vendor": true, "dist": true,
	"build": true, ".next": true, ".venv": true, "venv": true, "Pods": true, "DerivedData": true}

func (s *Scanner) instructions(inv *Inventory) {
	seen := map[string]bool{}
	add := func(path, project string, kind entry.Kind, scope entry.Scope) {
		if seen[path] {
			return
		}
		src, fi, ok := readFile(path)
		if !ok {
			return
		}
		seen[path] = true
		d := parseDoc(src)
		e := entry.Entry{Kind: kind, Scope: scope, Project: project, Path: path, Enabled: true,
			Name: filepath.Base(path), Description: d.fm.Description, Bytes: fi.Size(), Lines: d.lines, Modified: fi.ModTime().UTC().Format(time.RFC3339),
			Links: imports(d.body)}
		if kind == entry.Rule && d.fm.Paths != nil {
			e.Meta = map[string]any{"paths": d.fm.Paths}
		}
		inv.Entries = append(inv.Entries, e)
		s.followImports(inv, len(inv.Entries)-1, seen, 1)
	}

	cd := s.claudeDir()
	add(filepath.Join(cd, "CLAUDE.md"), "", entry.Instructions, entry.ScopeUser)
	for _, r := range globMD(filepath.Join(cd, "rules")) {
		add(r, "", entry.Rule, entry.ScopeUser)
	}
	repos := inv.repos()
	isRepo := map[string]bool{}
	for _, r := range repos {
		isRepo[r.Path] = true
	}
	for _, r := range repos {
		add(filepath.Join(r.Path, "CLAUDE.local.md"), r.Path, entry.Instructions, entry.ScopeLocal)
		add(filepath.Join(r.Path, ".claude", "CLAUDE.md"), r.Path, entry.Instructions, entry.ScopeProject)
		for _, rule := range globMD(filepath.Join(r.Path, ".claude", "rules")) {
			add(rule, r.Path, entry.Rule, entry.ScopeProject)
		}
		// CLAUDE.md / AGENTS.md at the root and in subdirectories (loaded when Claude works there).
		root := r.Path
		_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if de.IsDir() {
				if p == root {
					return nil
				}
				// A nested repo or project owns its own files (e.g. ~/dev is a project containing repos).
				if skipDirs[de.Name()] || isRepo[p] || exists(filepath.Join(p, ".git")) ||
					strings.Count(strings.TrimPrefix(p, root), string(os.PathSeparator)) > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			if n := de.Name(); n == "CLAUDE.md" || n == "AGENTS.md" {
				add(p, r.Path, entry.Instructions, entry.ScopeProject)
			}
			return nil
		})
	}
}

// followImports adds each @import target as its own entry and records missing ones as issues.
// It takes an index, not a pointer, because appending children can move the slice.
func (s *Scanner) followImports(inv *Inventory, parent int, seen map[string]bool, depth int) {
	links := inv.Entries[parent].Links
	parentPath, project := inv.Entries[parent].Path, inv.Entries[parent].Project
	issue := func(code string) { inv.Entries[parent].Issues = append(inv.Entries[parent].Issues, code) }
	for _, target := range links {
		p := s.expand(target, filepath.Dir(parentPath))
		if _, err := os.Stat(p); err != nil {
			issue("import-missing:" + target)
			continue
		}
		if depth > MaxImportDepth {
			issue("import-too-deep:" + target)
			continue
		}
		// Record every resolved import, even of a file already listed (e.g. CLAUDE.md: @AGENTS.md).
		e := &inv.Entries[parent]
		e.Meta = setMeta(e.Meta, "imports", append(metaStrings(e.Meta["imports"]), p))
		if seen[p] {
			continue
		}
		// Only Markdown is listed (and so shown, served to Claude or writable). An import of
		// anything else (`@~/.claude.json`, `@~/.aws/credentials`) is never read by cca.
		if !IsMarkdown(p) {
			issue("import-not-markdown:" + target)
			continue
		}
		src, fi, ok := readFile(p)
		if !ok {
			continue
		}
		seen[p] = true
		d := parseDoc(src)
		inv.Entries = append(inv.Entries, entry.Entry{Kind: entry.Instructions, Scope: entry.ScopeImport,
			Project: project, Path: p, Name: filepath.Base(p), Enabled: true, Bytes: fi.Size(), Lines: d.lines, Modified: fi.ModTime().UTC().Format(time.RFC3339),
			Links: imports(d.body), Meta: map[string]any{"importedBy": parentPath}})
		s.followImports(inv, len(inv.Entries)-1, seen, depth+1)
	}
}

func globMD(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
		return nil
	})
	return out
}
