package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/audit"
	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/install"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

// CLI runs `claude` in a directory (MCP scopes depend on it). Tests swap in a fake.
type CLI func(ctx context.Context, dir string, args ...string) ([]byte, error)

// ClaudeIn runs the real `claude` binary in dir with HOME set to home, so a --home sandbox
// changes the sandbox's config, never the real one.
func ClaudeIn(ctx context.Context, dir, home string, args ...string) ([]byte, error) {
	// A hung `claude` must not stall rescans or hold the write lock; installs download, so allow some time.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir
	env := os.Environ()
	if real, _ := os.UserHomeDir(); filepath.Clean(home) != filepath.Clean(real) {
		env = slices.DeleteFunc(env, func(v string) bool { return strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") })
	}
	cmd.Env = append(env, "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, cliError{verb: verb(args), out: strings.TrimSpace(string(out))}
	}
	return out, nil
}

// cliError is a failed `claude` command. It names only the subcommand: the arguments can hold
// a server definition with a pasted key, so they never reach an error, a log or the browser.
type cliError struct{ verb, out string }

func (e cliError) Error() string {
	if e.out == "" {
		return "claude " + e.verb + " failed"
	}
	return "claude " + e.verb + ": " + e.out
}

// verb is the subcommand (up to two words, before any flag or value), e.g. "mcp add-json".
func verb(a []string) string {
	n := min(len(a), 2)
	for i, w := range a[:n] {
		if strings.HasPrefix(w, "-") || strings.ContainsAny(w, "{/@\"") {
			n = i
			break
		}
	}
	return strings.Join(a[:n], " ")
}

type args map[string]any

func (a args) str(k string) string { s, _ := a[k].(string); return s }
func (a args) strs(k string) []string {
	var out []string
	if xs, ok := a[k].([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// errUser is shown to the user as-is.
type errUser struct{ msg string }

// errConfirm means a plugin installs by running a command the user hasn't seen yet. The app
// shows it; only a repeat with that command's sha256 runs it (never --yes).
type errConfirm struct {
	msg     string
	Command map[string]any `json:"command"`
	SHA     string         `json:"sha256"`
}

func (e errConfirm) Error() string { return e.msg }

func (e errUser) Error() string { return e.msg }

// entries returns the scanned entry path for each of paths, or an error if one isn't of these kinds.
func (s *Server) entries(paths []string, kinds ...entry.Kind) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		e, err := s.entry(p, kinds...)
		if err != nil {
			return nil, err
		}
		out = append(out, e.Path)
	}
	return out, nil
}

func (s *Server) entry(path string, kinds ...entry.Kind) (entry.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.inv.Entries {
		if e.Path != path {
			continue
		}
		for _, k := range kinds {
			if e.Kind == k {
				return e, nil
			}
		}
	}
	return entry.Entry{}, errUser{"That item isn't in the current scan. Reload and try again."}
}

func (s *Server) memoryPaths() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, e := range s.inv.Entries {
		if e.Kind == entry.Memory {
			out = append(out, e.Path)
		}
	}
	return out
}

// memoryDir returns the scanned memory folder equal to dir, or false. Callers use the returned
// path, never the one they were given, so a path from a request can't reach the disk.
func (s *Server) memoryDir(dir string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if g := s.globalMemoryDir(); dir == g { // Everywhere, even before its first memory
		return g, true
	}
	for _, p := range s.inv.Projects { // a project's first memory goes in its (not yet created) folder
		if d := filepath.Join(scan.ConfigDir(s.Home), "projects", p.Dir, "memory"); (p.Exists || p.Global) && !p.Worktree && d == dir {
			return d, true
		}
	}
	for _, e := range s.inv.Entries {
		if d := filepath.Dir(e.Path); (e.Kind == entry.Memory || e.Kind == entry.MemoryIndex) && d == dir {
			return d, true
		}
	}
	return "", false
}

func (s *Server) globalMemoryDir() string {
	return filepath.Join(scan.ConfigDir(s.Home), "projects", scan.Encode(s.Home), "memory")
}

func (s *Server) handleAct(w http.ResponseWriter, r *http.Request) {
	var req actRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeStatus(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if s.ReadOnly {
		writeStatus(w, http.StatusForbidden, "Read-only mode: start cca without --read-only to make changes.")
		return
	}
	s.writing.Lock()
	defer s.writing.Unlock()
	wr := write.New(s.Home)
	msg, inverse, err := s.do(r.Context(), wr, req.Op, args(req.Args))
	if c := (errConfirm{}); errors.As(err, &c) { // shown to the user; a repeat with its sha256 runs it
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": sentence(c.msg), "confirm": c})
		return
	}
	if err != nil {
		code := http.StatusInternalServerError
		var u errUser
		switch {
		case errors.As(err, &u), errors.Is(err, write.ErrOutside):
			code = http.StatusBadRequest
		case errors.Is(err, write.ErrLocked), errors.Is(err, write.ErrChanged):
			code = http.StatusConflict
		}
		writeStatus(w, code, sentence(err.Error()))
		return
	}
	s.settle(wr)
	act := s.logActivity(Activity{Who: "you", Title: titleFor(req.Op, args(req.Args)), Detail: detailFor(wr.Changes),
		Changes: wr.Changes, Inverse: inverse})
	s.Refresh(r.Context(), strings.HasPrefix(req.Op, "plugin-"))
	writeJSON(w, map[string]any{"message": msg, "activity": act.ID, "canUndo": act.CanUndo()})
}

// goneMemoryDir reports whether dir is the memory folder of a project whose folder is gone.
// goneMemoryDir returns the memory folder of a project whose folder no longer exists, if dir is one.
func (s *Server) goneMemoryDir(dir string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.inv.Projects {
		if d := filepath.Join(scan.ConfigDir(s.Home), "projects", p.Dir, "memory"); !p.Exists && !p.Global && d == dir {
			return d, true
		}
	}
	return "", false
}

// movedTo returns the folder a gone project's memories may move to: one the scan suggested for it.
func (s *Server) movedTo(from, to string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.state.Report.Findings {
		if f.Code == "folder-moved" && f.Path == from {
			for _, c := range f.Candidates {
				if c == to {
					return c, true
				}
			}
		}
	}
	return "", false
}

// cliDir is where a scoped `claude` command runs: home for user scope, or a known project
// folder (never an arbitrary path) for project and local scope. It returns that project too.
func (s *Server) cliDir(scope, project string) (string, string, error) {
	if scope == "user" {
		return s.Home, "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.inv.Projects {
		if p.Path == project && p.Exists && !p.Worktree && !p.Global {
			return p.Path, p.Path, nil
		}
	}
	return "", "", errUser{"Pick a project Claude has been used in."}
}

var pluginIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9_.-]*$`)

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// within reports whether path, with every existing symlink on it resolved, stays under dir.
func within(path, dir string) bool {
	real := func(p string) string {
		rest := ""
		for {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return filepath.Join(r, rest)
			}
			parent := filepath.Dir(p)
			if parent == p {
				return p
			}
			rest = filepath.Join(filepath.Base(p), rest)
			p = parent
		}
	}
	d := real(dir)
	return strings.HasPrefix(real(path), d+string(filepath.Separator))
}

func isDir(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

// lastJSON reads the result line `claude plugin … --json` prints last.
func lastJSON(out []byte) map[string]any {
	var r map[string]any
	_ = json.Unmarshal(scan.LastJSON(out), &r)
	if r == nil {
		r = map[string]any{}
	}
	return r
}

func orMessage(r map[string]any, err error) string {
	if m, _ := r["message"].(string); m != "" {
		return m
	}
	if err != nil {
		return cliMessage(err)
	}
	return "Claude Code didn't confirm the change."
}

// cliMessage keeps Claude Code's own words from a failed command.
func cliMessage(err error) string {
	if c := (cliError{}); errors.As(err, &c) && c.out != "" {
		return sentence(c.out)
	}
	return sentence(err.Error())
}

// memLock refuses memory writes while Claude Code is consolidating that memory folder.
func memLock(e entry.Entry) error {
	if e.Kind == entry.Memory || e.Kind == entry.MemoryIndex {
		return write.CheckLock(filepath.Dir(e.Path))
	}
	return nil
}

// do runs one op with wr, which records every file it touches. It returns the message to show
// and, for CLI changes, the op that would reverse it.
//
// Every op is all or nothing: if it fails part-way (a lock appears mid-rename, a bulk item is
// refused), the files it already changed are put back.
func (s *Server) do(ctx context.Context, wr *write.Writer, op string, a args) (_ string, _ *actRequest, err error) {
	defer func() {
		if err != nil && len(wr.Changes) > 0 {
			if uerr := wr.Undo(wr.Changes); uerr != nil {
				err = fmt.Errorf("%w (and some files couldn't be put back: %v)", err, uerr)
			}
		}
	}()
	md := []entry.Kind{entry.Memory}
	switch op {
	case "memory-save":
		e, err := s.entry(a.str("path"), md...)
		if err != nil {
			return "", nil, err
		}
		p, err := wr.SaveMemory(write.MemoryEdit{Path: e.Path, Title: a.str("name"), Type: a.str("type"),
			Description: a.str("description"), Body: a.str("body"), Seen: a.str("seen")}, s.memoryPaths())
		if err != nil {
			return "", nil, err
		}
		if p != e.Path {
			return "Saved and renamed. Links to it were updated.", nil, nil
		}
		return "Saved. Claude uses it next session.", nil, nil

	case "memory-trash":
		e, err := s.entry(a.str("path"), md...)
		if err != nil {
			return "", nil, err
		}
		if err := wr.TrashMemory(e.Path); err != nil {
			return "", nil, err
		}
		return "Moved to the Trash. Restore it from the Trash if you need it.", nil, nil

	case "memory-promote":
		e, err := s.entry(a.str("path"), md...)
		if err != nil {
			return "", nil, err
		}
		if _, err := wr.Move(e.Path, s.globalMemoryDir()); err != nil {
			return "", nil, err
		}
		return "Moved to Everywhere. Every project sees it now.", nil, nil

	case "create-stub", "memory-create":
		dir, ok := s.memoryDir(a.str("dir"))
		if !ok {
			return "", nil, errUser{"That memory folder isn't in the current scan."}
		}
		title := a.str("title")
		if title == "" {
			title = strings.ReplaceAll(strings.ReplaceAll(a.str("name"), "_", " "), "-", " ")
		}
		typ := a.str("type")
		if typ == "" {
			typ = "reference"
		}
		if _, err := wr.CreateMemory(dir, title, typ, a.str("description"), a.str("body")); err != nil {
			return "", nil, err
		}
		if a.str("body") == "" {
			return "Created " + title + ". Fill in the details when you're ready.", nil, nil
		}
		return "Created " + title + ".", nil, nil

	case "relink", "unlink":
		paths, err := s.entries(a.strs("paths"), md...)
		if err != nil {
			return "", nil, err
		}
		from, to := a.str("from"), a.str("to")
		if op == "unlink" {
			from, to = a.str("target"), ""
		} else if !s.isMemoryStem(to) {
			// The new target is written into each file, so it must name a real memory: anything else
			// (a suggestion from Claude, say) could slip instructions in as "a link".
			return "", nil, errUser{"Link to an existing memory."}
		}
		n, err := wr.Relink(paths, []string{from}, to)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Updated %d link%s.", n, plural(n)), nil, nil

	case "fix-frontmatter":
		e, err := s.entry(a.str("path"), md...)
		if err != nil {
			return "", nil, err
		}
		if err := wr.SetType(e.Path, ""); err != nil {
			return "", nil, err
		}
		return "Updated 1 memory.", nil, nil

	case "merge-global":
		pair, err := s.entries([]string{a.str("keep"), a.str("drop")}, md...)
		if err != nil {
			return "", nil, err
		}
		keep, drop := pair[0], pair[1]
		if keep == drop {
			return "", nil, errUser{"Pick two different memories to merge."}
		}
		// Trash the duplicate first, so a same-named file can then move into Everywhere.
		dropSrc, _ := scan.ReadText(drop)
		if err := wr.TrashMemory(drop); err != nil {
			return "", nil, err
		}
		g := keep
		if filepath.Dir(keep) != s.globalMemoryDir() {
			if g, err = wr.Move(keep, s.globalMemoryDir()); err != nil {
				return "", nil, err
			}
		}
		from := []string{strings.TrimSuffix(filepath.Base(drop), ".md")}
		if name, _, _, _ := scan.Fields(dropSrc); name != "" {
			from = append(from, name)
		}
		if _, err := wr.Relink(s.memoryPaths(), from, strings.TrimSuffix(filepath.Base(g), ".md")); err != nil {
			return "", nil, err
		}
		return "Merged into one memory in Everywhere.", nil, nil

	case "project-relocate":
		// A renamed or moved repo: carry its memories to the folder Claude Code now keys it by.
		from, ok := s.goneMemoryDir(a.str("from"))
		if !ok {
			return "", nil, errUser{"Only memories whose project folder no longer exists can be moved this way."}
		}
		to, ok := s.movedTo(from, filepath.Clean(a.str("to")))
		if !ok {
			return "", nil, errUser{"Pick one of the folders it may have moved to."}
		}
		if fi, err := os.Stat(to); err != nil || !fi.IsDir() || !filepath.IsAbs(to) {
			return "", nil, errUser{"Pick a folder that exists."}
		}
		dest := filepath.Join(scan.ConfigDir(s.Home), "projects", scan.Encode(to), "memory")
		des, err := os.ReadDir(from)
		if err != nil {
			return "", nil, err
		}
		n := 0
		for _, de := range des {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") || de.Name() == "MEMORY.md" {
				continue
			}
			if _, err := wr.Move(filepath.Join(from, de.Name()), dest); err != nil {
				return "", nil, err // do() puts back the ones already moved
			}
			n++
		}
		if idx := filepath.Join(from, "MEMORY.md"); write.IndexEmpty(idx) {
			if _, err := wr.Remove(idx); err != nil {
				return "", nil, err
			}
		}
		where := strings.Replace(to, s.Home, "~", 1)
		if to == s.Home {
			where = "Everywhere" // the home folder's memories load in every project
		}
		return fmt.Sprintf("Moved %d memor%s to %s. Claude loads them there from the next session.", n,
			ies(n), where), nil, nil

	case "mcp-add":
		var srv install.Server
		if b, err := json.Marshal(a["server"]); err != nil || json.Unmarshal(b, &srv) != nil {
			return "", nil, errUser{"That server definition isn't readable."}
		}
		scope := a.str("scope")
		if err := install.CheckServer(srv, scope); err != nil {
			return "", nil, errUser{sentence(err.Error())}
		}
		dir, project, err := s.cliDir(scope, a.str("project"))
		if err != nil {
			return "", nil, err
		}
		// An argument list, never a shell: nothing in the definition is interpreted.
		if _, err := s.cli()(ctx, dir, "mcp", "add-json", srv.Name, srv.JSON(), "--scope", scope); err != nil {
			return "", nil, errUser{cliMessage(err)}
		}
		return "Added MCP server " + srv.Name + ". New Claude sessions can use it.",
			&actRequest{Op: "mcp-remove", Args: map[string]any{"name": srv.Name, "scope": scope, "project": project}}, nil

	case "plugin-add":
		market, id, scope := a.str("marketplace"), a.str("plugin"), a.str("scope")
		if err := install.CheckPlugin(market, id, isDir); err != nil {
			return "", nil, errUser{sentence(err.Error())}
		}
		if !slices.Contains(install.Scopes, scope) {
			return "", nil, errUser{"Pick where the plugin applies."}
		}
		dir, _, err := s.cliDir(scope, a.str("project"))
		if err != nil {
			return "", nil, err
		}
		if market != "" {
			out, err := s.cli()(ctx, dir, "plugin", "marketplace", "add", "--json", market, "--scope", scope)
			if r := lastJSON(out); err != nil || r["outcome"] != "ok" {
				return "", nil, errUser{orMessage(r, err)}
			}
			if id == "" {
				return "Added the marketplace. Install one of its plugins as name@marketplace.", nil, nil
			}
		}
		cmd := []string{"plugin", "install", "--json", id, "--scope", scope}
		if sha := a.str("accept"); sha != "" {
			if !shaRe.MatchString(sha) {
				return "", nil, errUser{"That confirmation isn't valid."}
			}
			cmd = append(cmd, "--accept-command", sha) // only the exact command the user was shown
		}
		out, err := s.cli()(ctx, dir, cmd...)
		r := lastJSON(out)
		if shown, ok := r["shownCommand"].(map[string]any); ok && r["outcome"] != "ok" {
			sha, _ := shown["sha256"].(string)
			return "", nil, errConfirm{msg: orMessage(r, err), Command: shown, SHA: sha}
		}
		if err != nil || r["outcome"] != "ok" {
			return "", nil, errUser{orMessage(r, err)}
		}
		pid, _ := r["pluginId"].(string)
		if pid == "" {
			pid = id
		}
		_, project, _ := s.cliDir(scope, a.str("project"))
		return "Installed " + pid + ". New Claude sessions pick it up.",
			&actRequest{Op: "plugin-uninstall", Args: map[string]any{"id": pid, "scope": scope, "project": project}}, nil

	case "skill-create":
		sk := install.Skill{Name: a.str("name"), Description: a.str("description"), Body: a.str("body")}
		if err := install.CheckSkill(sk); err != nil {
			return "", nil, errUser{sentence(err.Error())}
		}
		root := scan.ConfigDir(s.Home)
		if a.str("scope") == "project" {
			_, project, err := s.cliDir("project", a.str("project"))
			if err != nil {
				return "", nil, err
			}
			root = filepath.Join(project, ".claude")
		}
		if strings.Contains(sk.Name, "..") || strings.ContainsAny(sk.Name, `/\`) { // CheckSkill allows a-z, 0-9 and -; this makes it plain here
			return "", nil, errUser{"A skill name uses lowercase letters, numbers and hyphens."}
		}
		path := filepath.Join(root, "skills", sk.Name, "SKILL.md")
		if !within(path, filepath.Dir(root)) {
			return "", nil, errUser{"That skills folder links somewhere else, so cca won't write there."}
		}
		if _, err := os.Stat(filepath.Dir(path)); err == nil {
			return "", nil, errUser{"A skill named " + sk.Name + " already exists there."}
		}
		if err := wr.File(path, []byte(install.SkillFile(sk))); err != nil {
			return "", nil, err
		}
		return "Created the " + sk.Name + " skill. Claude can use it from the next session.", nil, nil

	case "convert-to-agents":
		e, err := s.entry(a.str("path"), entry.Instructions)
		if err != nil {
			return "", nil, err
		}
		if e.Scope != entry.ScopeProject || e.Name != "CLAUDE.md" {
			return "", nil, errUser{"Only a project's CLAUDE.md can be converted."}
		}
		if m := scan.InstructionMode(s.Home, e.Project); m == scan.ModeClaudeOnly || m == scan.ModeManagedOnly {
			return "", nil, errUser{"Your instructionFiles setting (" + m + ") tells Claude Code not to read AGENTS.md, so converting would hide these instructions."}
		}
		b, _ := scan.ReadText(e.Path)
		if _, err := wr.ConvertToAgentsMD(e.Path); err != nil {
			return "", nil, err
		}
		msg := "Converted to AGENTS.md. Claude Code 2.1.277+ and other agent tools read it."
		if strings.Contains("\n"+string(b), "\n@") {
			msg += " Note: other tools don't follow its @imports."
		}
		return msg, nil, nil

	case "restore":
		e, err := s.entry(a.str("path"), entry.Memory, entry.MemoryIndex, entry.Instructions, entry.Rule, entry.Skill, entry.Command, entry.Agent)
		if err != nil {
			return "", nil, err
		}
		if err := memLock(e); err != nil {
			return "", nil, err
		}
		i := slices.IndexFunc(wr.Versions(e.Path), func(v write.Version) bool { return v.Path == a.str("version") })
		if i < 0 {
			return "", nil, errUser{"That version isn't in this file's history."}
		}
		if err := wr.Restore(e.Path, wr.Versions(e.Path)[i].Path); err != nil {
			return "", nil, err
		}
		return "Restored that version. Your previous text is kept as a version too.", nil, nil

	case "bulk":
		paths, err := s.entries(a.strs("paths"), md...)
		if err != nil {
			return "", nil, err
		}
		dir, ok := s.memoryDir(a.str("dir"))
		if a.str("action") == "move" && !ok {
			return "", nil, errUser{"That project has no memory folder yet."}
		}
		for _, p := range paths {
			var err error
			switch a.str("action") {
			case "trash":
				err = wr.TrashMemory(p)
			case "global":
				if filepath.Dir(p) != s.globalMemoryDir() {
					_, err = wr.Move(p, s.globalMemoryDir())
				}
			case "move":
				if filepath.Dir(p) != dir {
					_, err = wr.Move(p, dir)
				}
			case "type":
				if a.str("type") == "" { // SetType reads "" as keep the kind
					return "", nil, errUser{"Pick a kind."}
				}
				err = wr.SetType(p, a.str("type"))
			default:
				return "", nil, errUser{"Unknown bulk action."}
			}
			if err != nil {
				return "", nil, err // do() puts back the items already changed
			}
		}
		verb := map[string]string{"trash": "Moved %d memor%s to the Trash.", "global": "%d memor%s now load in every project.",
			"move": "Moved %d memor%s.", "type": "Changed the kind of %d memor%s."}[a.str("action")]
		return fmt.Sprintf(verb, len(paths), ies(len(paths))), nil, nil

	case "quiet-caps":
		e, err := s.entry(a.str("path"), entry.Instructions, entry.Rule, entry.Skill, entry.Command, entry.Agent)
		if err != nil {
			return "", nil, err
		}
		if e.Scope == entry.ScopePlugin {
			return "", nil, errUser{"Plugin files are replaced when the plugin updates."}
		}
		b, err := scan.ReadText(e.Path)
		if err != nil {
			return "", nil, err
		}
		out, n := audit.QuietCaps(string(b))
		if n == 0 {
			return "", nil, errUser{"Nothing to change."}
		}
		if err := wr.File(e.Path, []byte(out)); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Rewrote %d all-caps word%s at normal volume.", n, plural(n)), nil, nil

	case "file-save":
		e, err := s.entry(a.str("path"), entry.Skill, entry.Command, entry.Agent, entry.Instructions, entry.Rule, entry.Memory)
		if err != nil {
			return "", nil, err
		}
		if e.Scope == entry.ScopePlugin {
			return "", nil, errUser{"Plugin files are replaced when the plugin updates; edit a copy instead."}
		}
		if err := memLock(e); err != nil {
			return "", nil, err
		}
		if err := write.CheckUnchanged(e.Path, a.str("seen")); err != nil {
			return "", nil, err
		}
		return "Saved.", nil, wr.File(e.Path, []byte(a.str("content")))

	case "file-trash":
		e, err := s.entry(a.str("path"), entry.Skill, entry.Command, entry.Agent, entry.Rule)
		if err != nil {
			return "", nil, err
		}
		if e.Scope == entry.ScopePlugin {
			return "", nil, errUser{"This comes from a plugin. Turn the plugin off instead."}
		}
		target := e.Path
		if e.Kind == entry.Skill {
			target = filepath.Dir(e.Path) // a skill is its folder
		}
		if _, err := wr.Remove(target); err != nil {
			return "", nil, err
		}
		return "Moved " + e.Name + " to the Trash.", nil, nil

	case "make-global":
		groups := map[string][]string{}
		for _, p := range a.strs("paths") {
			e, err := s.entry(p, entry.Skill, entry.Command)
			if err != nil {
				return "", nil, err
			}
			if e.Scope != entry.ScopeProject {
				continue
			}
			k := string(e.Kind) + "\x00" + e.Name
			groups[k] = append(groups[k], e.Path)
		}
		moved := 0
		for _, g := range groups {
			if _, err := wr.MakeGlobal(g); err != nil {
				return "", nil, err
			}
			moved++
		}
		return fmt.Sprintf("Moved %d to Everywhere. The project copies are in the Trash; commit the removals in each repo.", moved), nil, nil

	case "hook-remove":
		path, event := a.str("path"), a.str("event")
		g, _ := a["group"].(float64)
		p, _ := a["pos"].(float64)
		s.mu.RLock()
		i := slices.IndexFunc(s.inv.Entries, func(h entry.Entry) bool {
			return h.Kind == entry.Hook && h.Path == path && h.Name == event && h.Scope != entry.ScopePlugin &&
				h.Meta["group"] == int(g) && h.Meta["pos"] == int(p)
		})
		var hook entry.Entry
		if i >= 0 {
			hook = s.inv.Entries[i]
		}
		s.mu.RUnlock()
		if i < 0 {
			return "", nil, errUser{"That hook wasn't found. Reload and try again."}
		}
		if err := wr.RemoveHookAt(hook.Path, hook.Name, int(g), int(p)); err != nil {
			return "", nil, err
		}
		return "Removed the hook. Undo it from Activity if you need it back.", nil, nil

	case "remove-hooks":
		needle := a.str("script")
		if needle == "" {
			return "", nil, errUser{"Nothing to remove."}
		}
		s.mu.RLock()
		files := map[string]bool{}
		for _, e := range s.inv.Entries {
			if e.Kind == entry.Hook && e.Scope != entry.ScopePlugin && slices.Contains(e.Issues, "hook-script-missing:"+needle) {
				files[e.Path] = true
			}
		}
		s.mu.RUnlock()
		n := 0
		for f := range files {
			k, err := wr.RemoveHooks(f, needle)
			if err != nil {
				return "", nil, err
			}
			n += k
		}
		if n == 0 {
			return "", nil, errUser{"That hook wasn't found. It may already be gone."}
		}
		return fmt.Sprintf("Removed %d hook%s. Undo it from Activity if you need it back.", n, plural(n)), nil, nil

	case "plugin-enable", "plugin-disable", "plugin-uninstall":
		id, scope, dir := a.str("id"), a.str("scope"), s.Home
		if scope == "project" || scope == "local" { // an install made here: undo it in its folder
			var err error
			if dir, _, err = s.cliDir(scope, a.str("project")); err != nil {
				return "", nil, err
			}
		} else {
			scope = ""
			s.mu.RLock()
			for _, e := range s.inv.Entries {
				if e.Kind == entry.Plugin && e.Meta["id"] == id && (a.str("scope") == "" || e.Meta["installScope"] == a.str("scope")) {
					scope, _ = e.Meta["installScope"].(string)
				}
			}
			s.mu.RUnlock()
		}
		if scope == "" || !pluginIDRe.MatchString(id) {
			return "", nil, errUser{"That plugin isn't installed."}
		}
		verb := strings.TrimPrefix(op, "plugin-")
		cmd := []string{"plugin", verb, id, "--json", "--scope", scope}
		if verb == "uninstall" {
			cmd = append(cmd, "--yes")
		}
		if _, err := s.cli()(ctx, dir, cmd...); err != nil {
			return "", nil, errUser{cliMessage(err)}
		}
		var inverse *actRequest
		if back := map[string]string{"enable": "plugin-disable", "disable": "plugin-enable"}[verb]; back != "" {
			inverse = &actRequest{Op: back, Args: map[string]any{"id": id}}
		}
		return map[string]string{"enable": "Turned on", "disable": "Turned off", "uninstall": "Uninstalled"}[verb] + " " + strings.Split(id, "@")[0] + ". New sessions pick it up.", inverse, nil

	case "mcp-remove":
		name, scope, project := a.str("name"), a.str("scope"), a.str("project")
		found := false
		s.mu.RLock()
		for _, e := range s.inv.Entries {
			if e.Kind == entry.MCP && e.Name == name && string(e.Scope) == scope && e.Project == project {
				found = true
			}
		}
		s.mu.RUnlock()
		if !found {
			return "", nil, errUser{"That MCP server isn't configured."}
		}
		if strings.HasPrefix(name, "-") { // a repo's .mcp.json picks this name; claude would read it as a flag
			return "", nil, errUser{"That server's name starts with -, so it can't be removed safely from here: delete it from the config file by hand."}
		}
		dir := s.Home
		if project != "" {
			dir = project
		}
		if _, err := s.cli()(ctx, dir, "mcp", "remove", name, "--scope", scope); err != nil {
			return "", nil, err
		}
		return "Removed MCP server " + name + ".", nil, nil
	}
	return "", nil, errUser{"That change isn't available."}
}

// handleReveal shows a known file or project folder in Finder or the file manager. It changes nothing, so it is
// allowed in read-only mode; unknown paths are refused.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req struct{ Path string }
	if err := decodeJSON(w, r, &req); err != nil {
		writeStatus(w, http.StatusBadRequest, "Bad request.")
		return
	}
	known := false
	s.mu.RLock()
	for _, e := range s.inv.Entries {
		known = known || e.Path == req.Path
	}
	for _, p := range s.inv.Projects {
		known = known || p.Path == req.Path
	}
	s.mu.RUnlock()
	if !known || s.Reveal == nil || !filepath.IsAbs(req.Path) { // relative would read as an option to open/xdg-open
		writeStatus(w, http.StatusNotFound, "Not something cca knows about.")
		return
	}
	if err := s.Reveal(req.Path); err != nil {
		writeStatus(w, http.StatusInternalServerError, "Couldn't open your file manager.")
		return
	}
	writeStatus(w, http.StatusOK, "")
}

var titles = map[string]string{
	"quiet-caps":  "Rewrote all-caps emphasis",
	"memory-save": "Edited a memory", "memory-trash": "Moved a memory to the Trash", "memory-promote": "Made a memory global",
	"create-stub": "Created a memory", "memory-create": "Created a memory", "relink": "Fixed a broken link", "unlink": "Removed a broken link",
	"fix-frontmatter": "Repaired a memory header", "merge-global": "Merged two memories", "file-save": "Edited a file",
	"file-trash": "Moved a file to the Trash", "make-global": "Made skills global", "remove-hooks": "Removed dead hooks",
	"hook-remove": "Removed a hook", "plugin-enable": "Turned on a plugin", "plugin-disable": "Turned off a plugin",
	"plugin-uninstall": "Uninstalled a plugin", "mcp-remove": "Removed an MCP server", "convert-to-agents": "Converted CLAUDE.md to AGENTS.md",
	"restore": "Restored an earlier version", "project-relocate": "Moved memories to a project's new folder",
	"mcp-add": "Added an MCP server", "plugin-add": "Installed a plugin", "skill-create": "Created a skill",
}

func titleFor(op string, a args) string {
	if op == "bulk" {
		n := strconv.Itoa(len(a.strs("paths")))
		return map[string]string{"trash": "Moved " + n + " memories to the Trash", "global": "Made " + n + " memories global",
			"move": "Moved " + n + " memories", "type": "Changed the kind of " + n + " memories"}[a.str("action")]
	}
	if t := titles[op]; t != "" {
		if id := a.str("id"); id != "" {
			return t + ": " + strings.Split(id, "@")[0]
		}
		return t
	}
	return op
}

// detailFor names the files a change touched, skipping indexes.
func detailFor(cs []write.Change) string {
	var names []string
	for _, c := range cs {
		n := strings.TrimSuffix(filepath.Base(c.Path), ".md")
		if n == strings.ToUpper(n) && n != "MEMORY" { // CLAUDE, AGENTS, SKILL: name the folder too
			n = filepath.Base(filepath.Dir(c.Path)) + "/" + filepath.Base(c.Path)
		}
		if n == "MEMORY" || slices.Contains(names, n) {
			continue
		}
		names = append(names, n)
	}
	if len(names) > 4 {
		names = append(names[:4], "+"+strconv.Itoa(len(names)-4)+" more")
	}
	return strings.Join(names, ", ")
}

func (s *Server) cli() CLI {
	if s.CLI != nil {
		return s.CLI
	}
	return func(ctx context.Context, dir string, a ...string) ([]byte, error) {
		return ClaudeIn(ctx, dir, s.Home, a...)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ies ends "memor": memory, memories.
func ies(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// sentence capitalises an error for display.
func sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// isMemoryStem reports whether name is the file name (without .md) of a memory.
func (s *Server) isMemoryStem(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.state.Entries {
		if e.Kind == entry.Memory && name != "" && strings.TrimSuffix(filepath.Base(e.Path), ".md") == name {
			return true
		}
	}
	return false
}
