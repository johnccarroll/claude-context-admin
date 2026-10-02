package write

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// MemTypes are the memory types Claude Code writes.
var MemTypes = map[string]bool{"feedback": true, "project": true, "reference": true, "user": true}

var typePrefix = regexp.MustCompile(`^(feedback|project|reference|user)_`)

// Slug turns a title into Claude Code's kebab-case memory name.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// SetFrontmatter sets name, description and type, keeping every other key and its order.
// The type stays where the file keeps it (top-level `type` or `metadata.type`): Claude Code has
// changed this shape without notice before, so cca never migrates it. New files use metadata.type,
// as current Claude Code writes. Invalid YAML is rebuilt from the lenient read.
func SetFrontmatter(src []byte, name, desc, typ string) ([]byte, error) {
	_, _, _, body := scan.Fields(src)
	var root yaml.Node
	head, _, _ := scan.SplitHeader(string(src))
	m := &yaml.Node{Kind: yaml.MappingNode}
	if head != "" && yaml.Unmarshal([]byte(head), &root) == nil && len(root.Content) == 1 && root.Content[0].Kind == yaml.MappingNode {
		m = root.Content[0]
	}
	set := func(parent *yaml.Node, key, val string) {
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				if v := parent.Content[i+1]; v.Kind == yaml.ScalarNode && v.Value == val {
					return // unchanged: keep its original quoting, so diffs stay minimal
				}
				parent.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: val}
				return
			}
		}
		parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Value: val})
	}
	del := func(parent *yaml.Node, key string) {
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
				return
			}
		}
	}
	set(m, "name", name)
	set(m, "description", desc)
	topLevel := false
	for i := 0; i+1 < len(m.Content); i += 2 {
		topLevel = topLevel || m.Content[i].Value == "type"
	}
	if typ != "" && topLevel {
		set(m, "type", typ)
	} else if typ != "" {
		var meta *yaml.Node
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "metadata" && m.Content[i+1].Kind == yaml.MappingNode {
				meta = m.Content[i+1]
			}
		}
		if meta == nil {
			del(m, "metadata")
			meta = &yaml.Node{Kind: yaml.MappingNode}
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "metadata"}, meta)
		}
		set(meta, "type", typ)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return []byte("---\n" + buf.String() + "---\n" + body), nil // body byte-for-byte: minimal diffs
}

// ---------- MEMORY.md index ----------

// IndexEmpty reports whether a MEMORY.md exists and lists no memories.
func IndexEmpty(index string) bool {
	b, err := readCapped(index)
	return err == nil && !strings.Contains(string(b), "](")
}

var sectionFor = map[string]string{"feedback": "## Feedback", "project": "## Project", "reference": "## Reference", "user": "## User"}

// indexLine is one line of MEMORY.md, which loads every session: line breaks in a title or hook
// would add lines of their own, so they collapse to spaces.
func indexLine(file, title, hook string) string {
	title, hook = strings.Join(strings.Fields(title), " "), strings.Join(strings.Fields(hook), " ")
	l := "- [" + title + "](" + file + ")"
	if hook != "" {
		l += " — " + hook
	}
	return l
}

// IndexUpsert sets the line for file, keeping its place, or adds it under the type's section.
func (w *Writer) IndexUpsert(index, file, title, hook, typ string) error {
	b, err := readCapped(index)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err // never rebuild an index we couldn't read
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = []string{"# Memory Index", ""}
	}
	want := indexLine(file, title, hook)
	for i, l := range lines {
		if isIndexLine(l, file) {
			lines[i] = want
			return w.File(index, []byte(strings.Join(lines, "\n")+"\n"))
		}
	}
	at := len(lines)
	if sec, ok := sectionFor[typ]; ok {
		for i, l := range lines {
			if strings.HasPrefix(l, sec) {
				at = i + 1
				for at < len(lines) && !strings.HasPrefix(lines[at], "#") {
					at++
				}
				for at > i+1 && strings.TrimSpace(lines[at-1]) == "" {
					at-- // stay above the blank line before the next section
				}
				break
			}
		}
	}
	lines = slices.Insert(lines, at, want)
	return w.File(index, []byte(strings.Join(lines, "\n")+"\n"))
}

// IndexRemove drops file's line from the index, if present.
func (w *Writer) IndexRemove(index, file string) error {
	b, err := readCapped(index)
	if err != nil {
		return nil
	}
	var out []string
	changed := false
	for _, l := range strings.Split(string(b), "\n") {
		if isIndexLine(l, file) {
			changed = true
			continue
		}
		out = append(out, l)
	}
	if !changed {
		return nil
	}
	return w.File(index, []byte(strings.Join(out, "\n")))
}

// ---------- links ----------

var linkRe = scan.Wikilink

// RewriteLinks points [[from]] links (any of the names, with or without .md) at to.
// An empty to turns the link into plain text. It returns the new text and how many changed.
func RewriteLinks(src string, from []string, to string) (string, int) {
	set := map[string]bool{}
	for _, f := range from {
		set[strings.TrimSuffix(f, ".md")] = true
	}
	n := 0
	out := linkRe.ReplaceAllStringFunc(src, func(m string) string {
		sm := linkRe.FindStringSubmatch(m)
		target := strings.TrimSuffix(strings.TrimSpace(sm[1]), ".md")
		if !set[target] {
			return m
		}
		n++
		if to == "" {
			if alias := strings.TrimPrefix(sm[2], "|"); alias != "" && !strings.HasPrefix(sm[2], "#") {
				return alias
			}
			return strings.ReplaceAll(strings.ReplaceAll(target, "_", " "), "-", " ")
		}
		return "[[" + to + sm[2] + "]]"
	})
	return out, n
}

// Relink rewrites links in each file; files without a matching link are left untouched.
func (w *Writer) Relink(paths, from []string, to string) (int, error) {
	total := 0
	for _, p := range paths {
		if err := CheckLock(filepath.Dir(p)); err != nil {
			return total, err
		}
		b, err := readCapped(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue // moved or trashed earlier in the same change
		}
		if err != nil {
			return total, err
		}
		out, n := RewriteLinks(string(b), from, to)
		if n == 0 {
			continue
		}
		if err := w.File(p, []byte(out)); err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// ---------- memories ----------

// MemoryEdit is a save from the editor. A non-empty Title becomes the kebab-case name; an empty
// Title keeps the current name (the editor sends one only when the user edited it).
type MemoryEdit struct {
	Path, Title, Type, Description, Body, Seen string
}

// SaveMemory writes the edit. When the name changes, the file is renamed to match (if it used
// Claude Code's {type}_{name} pattern), links across `all` memory files are rewritten, and the
// index line follows. It returns the memory's path after any rename.
func (w *Writer) SaveMemory(e MemoryEdit, all []string) (string, error) {
	dir := filepath.Dir(e.Path)
	if err := CheckLock(dir); err != nil {
		return "", err
	}
	if err := CheckUnchanged(e.Path, e.Seen); err != nil {
		return "", err
	}
	if e.Type != "" && !MemTypes[e.Type] {
		return "", fmt.Errorf("unknown memory type %q", e.Type)
	}
	src, err := readCapped(e.Path)
	if err != nil {
		return "", err
	}
	oldName, _, oldType, _ := scan.Fields(src)
	oldStem := strings.TrimSuffix(filepath.Base(e.Path), ".md")
	name := oldName
	if e.Title != "" {
		name = Slug(e.Title)
	}
	if name == "" { // {type}_{slug} file name → the kebab slug Claude Code writes in name:
		name = strings.ReplaceAll(typePrefix.ReplaceAllString(oldStem, ""), "_", "-")
	}
	if e.Type == "" {
		e.Type = oldType
	}
	body := strings.TrimLeft(e.Body, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	// The old header keeps keys the editor doesn't show (originSessionId, modified, ...).
	out, err := SetFrontmatter(append(headOf(src), []byte(body)...), name, e.Description, e.Type)
	if err != nil {
		return "", err
	}
	// Rename only when the user retitled it. A header that merely lacked a name gets one written
	// from the file name, and the file stays put.
	renamed := e.Title != "" && name != oldName
	newPath, newStem := e.Path, oldStem
	if renamed && (typePrefix.MatchString(oldStem) || oldStem == oldName) {
		if prefix := typePrefix.FindString(oldStem); prefix != "" {
			if e.Type != "" {
				prefix = e.Type + "_"
			}
			newStem = prefix + strings.ReplaceAll(name, "-", "_")
		} else {
			newStem = name
		}
		newPath = filepath.Join(dir, newStem+".md")
		if newPath != e.Path && exists(newPath) {
			return "", fmt.Errorf("a memory named %s already exists here", newStem)
		}
	}
	if err := w.File(newPath, out); err != nil {
		return "", err
	}
	index := filepath.Join(dir, "MEMORY.md")
	if newPath != e.Path {
		if _, err := w.Remove(e.Path); err != nil {
			return "", err
		}
		_ = w.IndexRemove(index, oldStem+".md")
	}
	if renamed { // links may target the stem or the old name
		var others []string
		for _, p := range all {
			if p != e.Path {
				others = append(others, p)
			}
		}
		var from []string
		if oldName != "" {
			from = append(from, oldName)
		}
		if newStem != oldStem {
			from = append(from, oldStem)
		}
		if _, err := w.Relink(others, from, newStem); err != nil {
			return newPath, err
		}
	}
	title := e.Title
	if title == "" {
		title = indexTitle(index, oldStem+".md", name)
	}
	return newPath, w.IndexUpsert(index, newStem+".md", title, e.Description, e.Type)
}

// headOf returns the raw frontmatter block (with delimiters), or an empty header.
func headOf(src []byte) []byte {
	if head, _, ok := scan.SplitHeader(string(src)); ok && head != "" {
		return []byte("---\n" + head + "\n---\n")
	}
	return []byte("---\n---\n")
}

// TrashMemory moves a memory to the Trash and removes its index line.
func (w *Writer) TrashMemory(path string) error {
	if err := CheckLock(filepath.Dir(path)); err != nil {
		return err
	}
	_ = w.IndexRemove(filepath.Join(filepath.Dir(path), "MEMORY.md"), filepath.Base(path))
	_, err := w.Remove(path)
	return err
}

// CreateMemory writes a new memory and its index line. It refuses to overwrite.
func (w *Writer) CreateMemory(dir, title, typ, desc, body string) (string, error) {
	if err := CheckLock(dir); err != nil {
		return "", err
	}
	if !MemTypes[typ] {
		return "", fmt.Errorf("unknown memory type %q", typ)
	}
	name := Slug(title)
	if name == "" {
		return "", fmt.Errorf("the title can't be empty")
	}
	stem := typ + "_" + strings.ReplaceAll(name, "-", "_")
	path := filepath.Join(dir, stem+".md")
	if exists(path) {
		return "", fmt.Errorf("a memory named %s already exists here", stem)
	}
	out, err := SetFrontmatter([]byte("---\n---\n"+body+"\n"), name, desc, typ)
	if err != nil {
		return "", err
	}
	if err := w.File(path, out); err != nil {
		return "", err
	}
	return path, w.IndexUpsert(filepath.Join(dir, "MEMORY.md"), stem+".md", title, desc, typ)
}

// Move moves a memory to another memory folder (another project, or the global one so every
// project sees it), updating both indexes. Links elsewhere keep resolving through the global folder.
func (w *Writer) Move(path, dir string) (string, error) {
	for _, d := range []string{filepath.Dir(path), dir} {
		if err := CheckLock(d); err != nil {
			return "", err
		}
	}
	dst := filepath.Join(dir, filepath.Base(path))
	if exists(dst) {
		return "", fmt.Errorf("that folder already has a memory named %s", filepath.Base(path))
	}
	src, err := readCapped(path)
	if err != nil {
		return "", err
	}
	name, desc, typ, _ := scan.Fields(src)
	if err := w.File(dst, src); err != nil {
		return "", err
	}
	title := indexTitle(filepath.Join(filepath.Dir(path), "MEMORY.md"), filepath.Base(path), name)
	if err := w.TrashMemory(path); err != nil {
		return "", err
	}
	return dst, w.IndexUpsert(filepath.Join(dir, "MEMORY.md"), filepath.Base(dst), title, desc, typ)
}

// SetType changes a memory's kind, keeping its name, summary, body and header shape. An empty
// typ keeps the kind, which just rewrites an unreadable header so it parses (e.g. quotes a
// description). A header that comes out the same isn't written.
func (w *Writer) SetType(path, typ string) error {
	if typ != "" && !MemTypes[typ] {
		return fmt.Errorf("unknown memory type %q", typ)
	}
	if err := CheckLock(filepath.Dir(path)); err != nil {
		return err
	}
	src, err := readCapped(path)
	if err != nil {
		return err
	}
	name, desc, cur, _ := scan.Fields(src)
	if typ == "" {
		typ = cur
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	out, err := SetFrontmatter(src, name, desc, typ)
	if err != nil {
		return err
	}
	if bytes.Equal(out, src) {
		return nil
	}
	return w.File(path, out)
}

// indexTitle is the title the index already uses for file, so a save doesn't rewrite it.
func indexTitle(index, file, fallback string) string {
	b, _ := readCapped(index)
	for _, l := range strings.Split(string(b), "\n") {
		if isIndexLine(l, file) {
			t := strings.TrimSpace(l)
			return t[3:strings.Index(t, "](")]
		}
	}
	return fallback
}

// isIndexLine reports whether l is MEMORY.md's line for file: a list item whose own link is to it.
// A description that merely mentions "(file)" doesn't count.
func isIndexLine(l, file string) bool {
	t := strings.TrimSpace(l)
	i := strings.Index(t, "](")
	return (strings.HasPrefix(t, "- [") || strings.HasPrefix(t, "* [")) && i > 0 && strings.HasPrefix(t[i+2:], file+")")
}
