// Package scan builds the inventory of everything Claude Code can load on this machine.
// It only reads. Secret values (MCP env and header values) are never kept.
package scan

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

// Runner runs the `claude` CLI. Tests swap in a fake.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Scanner reads a home directory. Home is injectable so tests never touch the real ~/.claude.
type Scanner struct {
	Home  string
	Run   Runner // nil skips plugin scanning
	Costs bool   // also run `claude plugin details` per plugin (slower)
	// Plugins, when set, keeps `claude plugin list` output while the files it reads are unchanged,
	// so a rescan (Claude Code rewrites ~/.claude.json all the time) doesn't start a `claude`.
	Plugins *PluginCache
}

// PluginCache is the last `claude plugin list --json` output and the state of the files behind it.
type PluginCache struct {
	mu       sync.Mutex
	key, out string
}

// Project is one ~/.claude/projects/<encoded> directory resolved back to its real path.
type Project struct {
	Dir      string `json:"dir"`  // encoded directory name
	Path     string `json:"path"` // real cwd, from the newest transcript; "" if unknown
	Exists   bool   `json:"exists"`
	Worktree bool   `json:"worktree"`
	Global   bool   `json:"global"` // the home directory itself
	Repo     bool   `json:"repo"`   // a git repository root
}

// Inventory is the full scan result.
type Inventory struct {
	Home     string        `json:"home"`
	Projects []Project     `json:"projects"`
	Entries  []entry.Entry `json:"entries"`
	Warnings []string      `json:"warnings,omitempty"`
}

func (s *Scanner) claudeDir() string { return ConfigDir(s.Home) }

// Scan runs every scanner. Individual failures become warnings, never a failed scan.
func (s *Scanner) Scan(ctx context.Context) *Inventory {
	inv := &Inventory{Home: s.Home, Entries: []entry.Entry{}} // [] not null in JSON, even for a new user
	inv.Projects = append([]Project{}, s.projects()...)
	s.memories(inv)
	s.instructions(inv)
	s.toolkit(inv)
	s.mcp(inv)
	s.hooks(inv)
	if s.Run != nil {
		s.plugins(ctx, inv)
	}
	sort.SliceStable(inv.Entries, func(i, j int) bool {
		a, b := inv.Entries[i], inv.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Path < b.Path
	})
	return inv
}

var cwdRe = regexp.MustCompile(`"cwd":"((?:[^"\\]|\\.)*)"`)

// projects resolves each encoded project dir from the cwd recorded in its newest transcript.
// The encoding (every / and . becomes -) can't be reversed, so the transcript is the source of truth.
func (s *Scanner) projects() []Project {
	root := filepath.Join(s.claudeDir(), "projects")
	des, _ := os.ReadDir(root)
	var out []Project
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		p := Project{Dir: de.Name()}
		p.Path = s.resolveDir(filepath.Join(root, de.Name()), de.Name())
		if p.Path != "" {
			_, err := os.Stat(p.Path)
			p.Exists = err == nil
			p.Global = filepath.Clean(p.Path) == filepath.Clean(s.Home)
			p.Worktree = strings.Contains(p.Path, "/.claude/worktrees/")
			p.Repo = exists(filepath.Join(p.Path, ".git"))
		}
		out = append(out, p)
	}
	return out
}

// resolveDir finds the real path of an encoded project dir. Repos get renamed and sessions cd
// into subfolders, so recorded cwds are checked against the encoding before being trusted.
func (s *Scanner) resolveDir(dir, encoded string) string {
	cwds := cwds(dir)
	// A session can cd into a subfolder, so the launch dir is the cwd that encodes to the dir name.
	for _, c := range cwds {
		if Encode(c) == encoded && exists(c) {
			return c
		}
	}
	if p := s.matchEncoded(encoded); p != "" {
		return p
	}
	for _, c := range cwds {
		if exists(c) {
			return c
		}
	}
	if len(cwds) > 0 {
		return cwds[0]
	}
	// No transcripts left (Claude Code prunes old ones): its session index remembers the folder.
	var idx struct {
		OriginalPath string `json:"originalPath"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sessions-index.json")); err == nil && json.Unmarshal(b, &idx) == nil &&
		Encode(idx.OriginalPath) == encoded {
		return idx.OriginalPath
	}
	return ""
}

// Encode mirrors Claude Code's project dir naming: every '/' and '.' becomes '-'.
func Encode(path string) string { return strings.NewReplacer("/", "-", ".", "-").Replace(path) }

// matchEncoded looks for a real directory up to three levels under home whose encoding matches.
func (s *Scanner) matchEncoded(encoded string) string {
	if Encode(s.Home) == encoded {
		return s.Home
	}
	found := ""
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if found != "" || depth > 3 || !strings.HasPrefix(encoded, Encode(dir)) {
			return
		}
		des, _ := os.ReadDir(dir)
		for _, de := range des {
			if !de.IsDir() || de.Name() == "Library" || skipDirs[de.Name()] {
				continue
			}
			p := filepath.Join(dir, de.Name())
			if Encode(p) == encoded {
				found = p
				return
			}
			walk(p, depth+1)
		}
	}
	walk(s.Home, 1)
	return found
}

// cwds returns the distinct cwds recorded in a project's transcripts, newest first.
func cwds(dir string) []string {
	des, _ := os.ReadDir(dir)
	type f struct {
		path string
		mod  int64
	}
	var files []f
	for _, de := range des {
		if strings.HasSuffix(de.Name(), ".jsonl") {
			if fi, err := de.Info(); err == nil {
				files = append(files, f{filepath.Join(dir, de.Name()), fi.ModTime().UnixNano()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	var out []string
	seen := map[string]bool{}
	for i, fl := range files {
		if i >= 20 { // enough history to survive a rename
			break
		}
		if c := firstCwd(fl.path); c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func firstCwd(path string) string {
	fh, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 0; sc.Scan() && n < 200; n++ {
		if m := cwdRe.FindSubmatch(sc.Bytes()); m != nil {
			return strings.ReplaceAll(string(m[1]), `\\`, `\`)
		}
	}
	return ""
}

// repos are real, non-worktree project directories whose files we scan for project-scope config.
func (inv *Inventory) repos() []Project {
	var out []Project
	seen := map[string]bool{}
	for _, p := range inv.Projects {
		tmp := strings.HasPrefix(p.Path, "/private/tmp") || strings.HasPrefix(p.Path, "/tmp")
		if !p.Exists || p.Global || p.Worktree || (tmp && !strings.HasPrefix(p.Path, inv.Home)) || seen[p.Path] {
			continue
		}
		seen[p.Path] = true
		out = append(out, p)
	}
	return out
}

func (s *Scanner) expand(p, base string) string {
	switch {
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(s.Home, p[2:])
	case filepath.IsAbs(p):
		return p
	default:
		return filepath.Join(base, p)
	}
}

func readFile(path string) ([]byte, os.FileInfo, bool) {
	if !IsMarkdown(path) { // notes.md linked to a credentials file is not Markdown: never listed or served
		return nil, nil, false
	}
	b, err := ReadText(path)
	if err != nil {
		return nil, nil, false
	}
	fi, _ := os.Stat(path)
	return b, fi, true
}

// MaxText caps how much of one instructions, memory or skill file is read. Real ones are a few
// KB; the cap keeps a hostile repo (`@/dev/zero`, a symlink to a device) from hanging cca.
const MaxText = 2 << 20

// ReadText reads a regular file (symlinks followed), at most MaxText bytes. Devices, FIFOs and
// folders are refused.
func ReadText(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxText))
}

// Snippet is up to about 200 characters of text around the first case-insensitive match of q.
// It works on runes, so a character whose lowercase form is longer can't shift the cut.
func Snippet(text, q string) string {
	if _, body, ok := SplitHeader(text); ok { // the header is shown as title and summary already
		text = body
	}
	r := []rune(text)
	low := []rune(strings.ToLower(text))
	i := 0
	if q != "" && len(low) == len(r) {
		if j := strings.Index(string(low), q); j >= 0 {
			i = len([]rune(string(low)[:j]))
		}
	}
	start, end := max(0, i-80), min(len(r), i+len([]rune(q))+120)
	return strings.Join(strings.Fields(string(r[start:end])), " ")
}

func (inv *Inventory) warn(msg string) { inv.Warnings = append(inv.Warnings, msg) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// IsMarkdown reports whether path is a .md file and, through any symlinks, still one: a link named
// notes.md that points at a credentials file is not Markdown.
func IsMarkdown(path string) bool {
	r, err := filepath.EvalSymlinks(path)
	return err == nil && strings.EqualFold(filepath.Ext(path), ".md") && strings.EqualFold(filepath.Ext(r), ".md")
}

// ConfigDir is Claude Code's user folder: $CLAUDE_CONFIG_DIR when it's set, else ~/.claude. A
// --home sandbox always uses its own ~/.claude.
func ConfigDir(home string) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		if real, _ := os.UserHomeDir(); filepath.Clean(real) == filepath.Clean(home) {
			return d
		}
	}
	return filepath.Join(home, ".claude")
}

// GlobalConfig is the file holding user and local MCP servers: ~/.claude.json, or .claude.json
// inside CLAUDE_CONFIG_DIR.
func GlobalConfig(home string) string {
	if d := ConfigDir(home); d != filepath.Join(home, ".claude") {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}
