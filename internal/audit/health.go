package audit

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// Instruction-health checks: the mechanical subset of the /claude-api prompt-audit guide.
// Judgment calls (cruft, conflicts between files) are left to that audit, run by Claude.

const (
	capsThreshold = 5         // stacked emphasis worth flagging in one file
	bigSkillLines = 500       // a skill this long costs a lot every time it fires
	bigSkillBytes = 30 * 1024 // same, by size
)

var (
	capsRe     = regexp.MustCompile(`\b(MUST NOT|MUST|NEVER|ALWAYS|CRITICAL|IMPORTANT|REQUIRED|DO NOT)\b`)
	scaffoldRe = regexp.MustCompile(`(?i)think step by step|take a deep breath|<scratchpad>|don'?t be lazy|do not be lazy`)
	retiredRe  = regexp.MustCompile(`(?i)\bclaude[- ](2|instant|3)(\b|[.-])|\b(sonnet|opus|haiku)[- ]3(\.[57])?\b`)
	pathRe     = regexp.MustCompile("(?:^|[\\s`(\"'])(~/[\\w.@/-]+|/Users/[\\w.@/-]+)")
)

// Evidence is one line a health finding points at.
type Evidence struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

func health(homeDir string, es []entry.Entry) []Finding {
	var out []Finding
	for _, e := range es {
		switch e.Kind {
		case entry.Instructions, entry.Rule, entry.Skill, entry.Command, entry.Agent:
		default:
			continue
		}
		if e.Scope == entry.ScopePlugin {
			continue // plugin files are replaced on update; not the user's to edit
		}
		b, err := scan.ReadText(e.Path)
		if err != nil {
			continue
		}
		raw := strings.Split(string(b), "\n")
		var caps, scaffold, retired, paths []Evidence
		for _, l := range proseLines(string(b)) {
			shown := Evidence{l.Line, raw[l.Line-1]} // match without inline code, show the whole line
			if capsRe.MatchString(l.Text) {
				caps = append(caps, shown)
			}
			if scaffoldRe.MatchString(l.Text) {
				scaffold = append(scaffold, shown)
			}
			if retiredRe.MatchString(l.Text) {
				retired = append(retired, shown)
			}
			for _, m := range pathRe.FindAllStringSubmatch(l.Text, -1) {
				p := strings.TrimRight(m[1], ".,:;)")
				if strings.ContainsAny(p, "<>*$") {
					continue
				}
				full := strings.Replace(p, "~", homeDir, 1)
				if Private(full, homeDir) {
					continue // can't check without macOS asking for that folder; don't probe it
				}
				if !exists(full) {
					paths = append(paths, Evidence{l.Line, p})
				}
			}
		}
		add := func(code, conf string, ev []Evidence) {
			out = append(out, Finding{Code: code, Path: e.Path, Project: e.Project, Confidence: conf, Evidence: ev[:min(len(ev), 6)], Detail: strconv.Itoa(len(ev))})
		}
		if len(caps) >= capsThreshold {
			add("pressure-language", "Medium", caps)
		}
		if len(scaffold) > 0 {
			add("dated-scaffold", "Medium", scaffold)
		}
		if len(retired) > 0 {
			add("retired-model", "Medium", retired)
		}
		if len(paths) > 0 {
			add("missing-path", "Low", paths)
		}
		if e.Kind == entry.Skill && (e.Lines > bigSkillLines || e.Bytes > bigSkillBytes) {
			out = append(out, Finding{Code: "verbose-skill", Path: e.Path, Project: e.Project, Confidence: "Medium", Detail: strconv.Itoa(e.Lines)})
		}
	}
	return out
}

// proseLines returns numbered lines outside fenced code blocks and frontmatter, with inline code
// removed, so examples and commands are never flagged or rewritten.
func proseLines(src string) []Evidence {
	var out []Evidence
	fence, front := false, strings.HasPrefix(src, "---\n")
	for i, l := range strings.Split(src, "\n") {
		t := strings.TrimSpace(l)
		if front {
			if i > 0 && t == "---" {
				front = false
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		out = append(out, Evidence{i + 1, inlineCode.ReplaceAllString(l, "")})
	}
	return out
}

var inlineCode = regexp.MustCompile("`[^`]*`")

// QuietCaps rewrites stacked all-caps emphasis at normal volume (MUST → must, a leading NEVER →
// Never), outside code. It returns the new text and how many words changed.
func QuietCaps(src string) (string, int) {
	lines := strings.Split(src, "\n")
	n := 0
	for _, l := range proseLines(src) {
		orig := lines[l.Line-1]
		// rewrite only outside inline code: split on backticks and touch even segments
		segs := strings.Split(orig, "`")
		for i := 0; i < len(segs); i += 2 {
			segs[i] = capsRe.ReplaceAllStringFunc(segs[i], func(w string) string {
				n++
				low := strings.ToLower(w)
				if sentenceStart(segs[i], w) {
					return strings.ToUpper(low[:1]) + low[1:]
				}
				return low
			})
		}
		lines[l.Line-1] = strings.Join(segs, "`")
	}
	return strings.Join(lines, "\n"), n
}

// sentenceStart: nothing but markers, numbers, symbols or emoji come before the word on its line
// ("- ❌ **NEVER**"), or it follows ". " / ": ".
func sentenceStart(line, word string) bool {
	i := strings.Index(line, word)
	if i < 0 {
		return false
	}
	before := line[:i]
	if !strings.ContainsFunc(before, unicode.IsLetter) {
		return true
	}
	t := strings.TrimRight(before, " *_")
	return strings.HasSuffix(t, ".") || strings.HasSuffix(t, ":")
}

// privateDirs are folders macOS guards with a permission prompt (Files and Folders). Checking
// whether a mentioned path exists isn't worth asking the user for access to them.
var privateDirs = []string{"Desktop", "Documents", "Downloads", "Pictures", "Movies", "Music", "Library/Mobile Documents",
	"Library/CloudStorage", "Library/Group Containers", "Library/Containers", "Library/Mail", "Library/Messages", "Library/Calendars"}

// Private reports whether p is in a folder macOS guards with a permission prompt (or a volume).
func Private(p, home string) bool {
	if strings.HasPrefix(p, "/Volumes/") {
		return true
	}
	for _, d := range privateDirs {
		if dir := filepath.Join(home, d); p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}
