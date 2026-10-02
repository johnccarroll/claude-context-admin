package scan

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatter holds the fields Claude Code writes, in both the current
// (`metadata.type`) and legacy (top-level `type`) shapes.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Metadata    struct {
		Type string `yaml:"type"`
	} `yaml:"metadata"`
	Paths any `yaml:"paths"` // rules: glob scoping
}

type doc struct {
	fm      frontmatter
	hasFM   bool
	body    string
	lines   int
	badYAML bool
}

// SplitHeader splits a markdown file into its YAML header and body. An empty header
// ("---\n---") counts as a header, and Windows line endings are read as plain newlines.
func SplitHeader(s string) (head, body string, ok bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", s, false
	}
	rest := s[3:] // keeps the newline after the opening ---, so an empty header matches too
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", s, false
	}
	if end > 0 {
		head = rest[1:end]
	}
	return head, strings.TrimPrefix(rest[end+4:], "\n"), true
}

func parseDoc(src []byte) doc {
	d := doc{lines: bytes.Count(src, []byte("\n"))}
	head, body, ok := SplitHeader(string(src))
	d.body = body
	if !ok {
		return d
	}
	d.hasFM = true
	if err := yaml.Unmarshal([]byte(head), &d.fm); err != nil {
		// Keep the finding, but still read simple `key: value` lines the way a lenient loader would.
		d.badYAML = true
		for _, ln := range strings.Split(head, "\n") {
			k, v, ok := strings.Cut(ln, ":")
			v = strings.TrimSpace(v)
			switch {
			case !ok:
			case k == "name":
				d.fm.Name = v
			case k == "description":
				d.fm.Description = v
			case k == "type":
				d.fm.Type = v
			case strings.TrimSpace(k) == "type":
				d.fm.Metadata.Type = v
			}
		}
		return d
	}
	return d
}

func (d doc) memType() string {
	if d.fm.Metadata.Type != "" {
		return d.fm.Metadata.Type
	}
	return d.fm.Type
}

var wikilink = regexp.MustCompile(`\[\[([^\]|#]+)`)

// wikilinks returns raw [[link]] targets, trimmed, with a trailing .md removed.
func wikilinks(body string) []string {
	var out []string
	for _, m := range wikilink.FindAllStringSubmatch(body, -1) {
		out = append(out, strings.TrimSuffix(strings.TrimSpace(m[1]), ".md"))
	}
	return out
}

// imports returns @import targets: lines whose first token starts with "@".
// Paths may contain spaces and apostrophes, so the whole rest of the line is the path.
func imports(body string) []string {
	var out []string
	inFence := false
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(t, "@") || len(t) < 2 {
			continue
		}
		p := strings.ReplaceAll(t[1:], `\ `, " ")
		// A path, or a bare file name with an extension (@AGENTS.md); not an @mention.
		if strings.ContainsAny(p[:1], "/~.") || strings.Contains(p, "/") || filepath.Ext(p) != "" && !strings.Contains(p, " ") {
			out = append(out, p)
		}
	}
	return out
}

// Fields is a lenient read of a markdown file's name, description, memory type and body.
// It tolerates invalid YAML the way Claude Code does, so writers can repair such files.
func Fields(src []byte) (name, description, memType, body string) {
	d := parseDoc(src)
	return d.fm.Name, d.fm.Description, d.memType(), d.body
}

// HeaderProblem describes why a memory file's header won't read cleanly, or returns "".
func HeaderProblem(src []byte) string {
	d := parseDoc(src)
	switch {
	case !d.hasFM:
		return "it has no header (--- name / description / type ---)"
	case d.badYAML:
		return "its header isn't valid YAML (quote the description if it contains quotes or a colon)"
	}
	return ""
}
