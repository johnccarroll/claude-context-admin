package audit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// movedFolders finds project memories whose folder no longer exists, usually a renamed or moved
// repo. Claude Code keys memory by folder path, so these load in no project at all. The finding
// lists folders next to the old one that have no memories yet, best guess first: the folder whose
// name the memories mention most.
func movedFolders(inv *scan.Inventory) []Finding {
	var out []Finding
	for _, p := range inv.Projects {
		if p.Exists || p.Worktree || p.Global || p.Path == "" {
			continue // worktrees are throwaway; an unknown path can't be matched
		}
		dir := filepath.Join(scan.ConfigDir(inv.Home), "projects", p.Dir, "memory")
		var text strings.Builder
		n := 0
		for _, e := range inv.Entries {
			if e.Kind == entry.Memory && filepath.Dir(e.Path) == dir {
				n++
				b, _ := scan.ReadText(e.Path)
				text.WriteString(strings.ToLower(string(b)))
			}
		}
		if n == 0 {
			continue
		}
		type cand struct {
			path  string
			score int
		}
		var cands []cand
		parent := filepath.Dir(p.Path)
		des, _ := os.ReadDir(parent)
		for _, de := range des {
			c := filepath.Join(parent, de.Name())
			if !de.IsDir() || strings.HasPrefix(de.Name(), ".") || hasMemories(inv.Home, c) {
				continue
			}
			cands = append(cands, cand{c, strings.Count(text.String(), strings.ToLower(de.Name()))})
		}
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
		f := Finding{Code: "folder-moved", Path: dir, Project: p.Path, Detail: ""}
		if len(cands) > 0 && cands[0].score > 0 {
			f.Detail = cands[0].path
		}
		for i, c := range cands {
			if i == 12 {
				break
			}
			f.Candidates = append(f.Candidates, c.path)
		}
		out = append(out, f)
	}
	return out
}

func hasMemories(home, folder string) bool {
	des, _ := os.ReadDir(filepath.Join(scan.ConfigDir(home), "projects", scan.Encode(folder), "memory"))
	for _, de := range des {
		if strings.HasSuffix(de.Name(), ".md") && de.Name() != "MEMORY.md" {
			return true
		}
	}
	return false
}
