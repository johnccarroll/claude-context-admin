package write

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// RemoveHooks deletes every hook in a settings file whose command mentions needle, dropping
// emptied matcher groups and events. Other settings are kept; key order is normalised.
// The previous version is kept in cca's history (0600, user-only), like every other change.
func (w *Writer) RemoveHooks(settings, needle string) (int, error) {
	b, err := readCapped(settings)
	if err != nil {
		return 0, err
	}
	var doc map[string]any
	if err := decode(b, &doc); err != nil {
		return 0, fmt.Errorf("%s isn't valid JSON, so it was left alone", filepath.Base(settings))
	}
	hooks, _ := doc["hooks"].(map[string]any)
	removed := 0
	for ev, groups := range hooks {
		gs, _ := groups.([]any)
		var keepGroups []any
		for _, g := range gs {
			gm, _ := g.(map[string]any)
			hs, _ := gm["hooks"].([]any)
			var keep []any
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				if cmd, _ := hm["command"].(string); needle != "" && strings.Contains(cmd, needle) {
					removed++
					continue
				}
				keep = append(keep, h)
			}
			if len(keep) > 0 {
				gm["hooks"] = keep
				keepGroups = append(keepGroups, gm)
			}
		}
		if len(keepGroups) > 0 {
			hooks[ev] = keepGroups
		} else {
			delete(hooks, ev)
		}
	}
	if removed == 0 {
		return 0, nil
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	}
	out, err := encode(doc)
	if err != nil {
		return 0, err
	}
	return removed, w.File(settings, out)
}

// RemoveHookAt deletes the hook at hooks[event][group].hooks[pos], as recorded by the scan.
func (w *Writer) RemoveHookAt(settings, event string, group, pos int) error {
	b, err := readCapped(settings)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := decode(b, &doc); err != nil {
		return fmt.Errorf("%s isn't valid JSON, so it was left alone", filepath.Base(settings))
	}
	hooks, _ := doc["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	if group >= len(groups) {
		return fmt.Errorf("that hook moved; reload and try again")
	}
	gm, _ := groups[group].(map[string]any)
	hs, _ := gm["hooks"].([]any)
	if pos >= len(hs) {
		return fmt.Errorf("that hook moved; reload and try again")
	}
	hs = append(hs[:pos], hs[pos+1:]...)
	if len(hs) > 0 {
		gm["hooks"] = hs
	} else {
		groups = append(groups[:group], groups[group+1:]...)
	}
	if len(groups) > 0 {
		hooks[event] = groups
	} else {
		delete(hooks, event)
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	}
	out, err := encode(doc)
	if err != nil {
		return err
	}
	return w.File(settings, out)
}

// ConvertToAgentsMD turns a project's CLAUDE.md into AGENTS.md (read by Claude Code 2.1.277+ and
// by other agent tools). The old file goes to the Trash. It refuses if AGENTS.md already exists.
func (w *Writer) ConvertToAgentsMD(claudeMD string) (string, error) {
	if filepath.Base(claudeMD) != "CLAUDE.md" {
		return "", fmt.Errorf("only a CLAUDE.md can be converted")
	}
	if fi, err := os.Lstat(claudeMD); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		// Its target may be outside the repo (or private): copying it in as a regular AGENTS.md
		// could put that text where it gets committed.
		return "", fmt.Errorf("CLAUDE.md here is a link to another file; convert that file instead")
	}
	dst := filepath.Join(filepath.Dir(claudeMD), "AGENTS.md")
	if exists(dst) {
		return "", fmt.Errorf("this folder already has an AGENTS.md; merge the two by hand")
	}
	b, err := readCapped(claudeMD)
	if err != nil {
		return "", err
	}
	if err := w.File(dst, b); err != nil {
		return "", err
	}
	if _, err := w.Remove(claudeMD); err != nil {
		return dst, err
	}
	return dst, nil
}

// MakeGlobal moves identical project copies of a skill or command into the user's ~/.claude,
// so every project shares one copy. The first copy is installed; all project copies go to the
// Trash. It refuses if a user-scope one already exists with different contents.
func (w *Writer) MakeGlobal(copies []string) (string, error) {
	if len(copies) == 0 {
		return "", fmt.Errorf("nothing to move")
	}
	first := copies[0]
	want, err := readCapped(first)
	if err != nil {
		return "", err
	}
	for _, c := range copies[1:] {
		b, err := readCapped(c)
		if err != nil || string(b) != string(want) {
			return "", fmt.Errorf("%s differs from the other copies; merge them by hand first", c)
		}
	}
	for _, c := range copies { // copies that are links to one shared folder: moving them would only trash the links
		for _, p := range []string{c, filepath.Dir(c)} {
			if fi, err := os.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
				return "", fmt.Errorf("%s is a link to a shared copy; nothing to move", p)
			}
		}
	}
	claudeDir := scan.ConfigDir(w.Home)
	var src, dst string
	if filepath.Base(first) == "SKILL.md" { // skills are folders: move the whole folder
		src = filepath.Dir(first)
		dst = filepath.Join(claudeDir, "skills", filepath.Base(src))
	} else { // commands: keep the path under commands/ (subfolders are namespaces)
		i := strings.LastIndex(first, string(filepath.Separator)+"commands"+string(filepath.Separator))
		if i < 0 {
			return "", fmt.Errorf("%s isn't a skill or command", first)
		}
		src = first
		dst = filepath.Join(claudeDir, first[i+1:])
	}
	if err := w.inside(dst); err != nil {
		return "", err
	}
	if exists(dst) {
		if !sameTree(src, dst) {
			return "", fmt.Errorf("you already have a different %s everywhere; compare them first", filepath.Base(dst))
		}
	} else if err := w.copyTree(src, dst); err != nil {
		return "", err
	}
	for _, c := range copies {
		target := c
		if filepath.Base(c) == "SKILL.md" {
			target = filepath.Dir(c)
		}
		if _, err := w.Remove(target); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

func (w *Writer) copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // links (whose targets could be anywhere) and special files aren't copied
		}
		b, err := readCapped(p)
		if err != nil {
			return err
		}
		if err := w.File(to, b); err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(to, fi.Mode().Perm()) // keep skill scripts executable
	})
}

// sameTree reports whether every file under a has the same content under b. A folder it can't
// walk counts as different, so nothing is removed on a guess.
func sameTree(a, b string) bool {
	same := true
	err := filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(a, p)
		x, _ := readCapped(p)
		y, err := readCapped(filepath.Join(b, rel))
		if err != nil || string(x) != string(y) {
			same = false
		}
		return nil
	})
	return same && err == nil
}

// decode and encode round-trip a settings file without changing what it says: numbers stay
// exact (json.Number) and "&&", "<" or ">" in hook commands aren't escaped to \u0026 and friends.
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil // with the trailing newline a file should end in
}
