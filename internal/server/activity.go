package server

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/proposal"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

// Activity is one change to Claude's memory or setup, made in cca or seen on disk.
type Activity struct {
	ID      string         `json:"id"`
	At      time.Time      `json:"at"`
	Who     string         `json:"who"` // "you" or "claude"
	Title   string         `json:"title"`
	Detail  string         `json:"detail,omitempty"`
	Changes []write.Change `json:"changes,omitempty"`
	Inverse *actRequest    `json:"inverse,omitempty"` // for CLI changes: the op that reverses it
	Undone  bool           `json:"undone,omitempty"`
}

// CanUndo: file changes can always be reversed from versions; CLI changes need an inverse op.
func (a Activity) CanUndo() bool { return !a.Undone && (len(a.Changes) > 0 || a.Inverse != nil) }

const keepActivity = 1000

var activityMu sync.Mutex

func (s *Server) activityPath() string {
	return filepath.Join(write.DataDir(s.Home), "activity.jsonl")
}

func (s *Server) loadActivity() []Activity {
	f, err := os.Open(s.activityPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Activity
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var a Activity
		if json.Unmarshal(sc.Bytes(), &a) == nil {
			out = append(out, a)
		}
	}
	return out
}

func (s *Server) saveActivity(all []Activity) error {
	if len(all) > keepActivity {
		all = all[len(all)-keepActivity:]
	}
	var b strings.Builder
	for _, a := range all {
		j, _ := json.Marshal(a)
		b.Write(j)
		b.WriteByte('\n')
	}
	return write.Atomic(s.activityPath(), []byte(b.String()), 0o600)
}

// logActivity appends a, filling in ID and time, and returns it.
func (s *Server) logActivity(a Activity) Activity {
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	a.ID, a.At = hex.EncodeToString(id), time.Now().UTC()
	activityMu.Lock()
	defer activityMu.Unlock()
	_ = s.saveActivity(append(s.loadActivity(), a))
	return a
}

type activityView struct {
	Activity
	CanUndo bool `json:"canUndo"`
}

func (s *Server) handleActivity(w http.ResponseWriter, _ *http.Request) {
	activityMu.Lock()
	all := s.loadActivity()
	activityMu.Unlock()
	out := []activityView{}
	for i := len(all) - 1; i >= 0 && len(out) < 200; i-- {
		out = append(out, activityView{all[i], all[i].CanUndo()})
	}
	writeJSON(w, out)
}

// handleUndo reverses one activity. The undo is itself logged (and can be undone).
func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID string }
	if err := decodeJSON(w, r, &req); err != nil {
		writeStatus(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if s.ReadOnly {
		writeStatus(w, http.StatusForbidden, "Read-only mode: start cca without --read-only to make changes.")
		return
	}
	s.writing.Lock() // also makes check-then-undo atomic, so a double click can't undo twice
	defer s.writing.Unlock()
	activityMu.Lock()
	all := s.loadActivity()
	activityMu.Unlock()
	i := slices.IndexFunc(all, func(a Activity) bool { return a.ID == req.ID })
	if i < 0 || !all[i].CanUndo() {
		writeStatus(w, http.StatusBadRequest, "That change can't be undone (it may already be undone).")
		return
	}
	a := all[i]
	wr := write.New(s.Home)
	var err error
	if a.Inverse != nil && !slices.Contains([]string{"mcp-remove", "plugin-uninstall", "plugin-enable", "plugin-disable"}, a.Inverse.Op) {
		err = errors.New("that change's record isn't one cca makes") // activity.jsonl could have been edited
	} else if a.Inverse != nil {
		_, _, err = s.do(r.Context(), wr, a.Inverse.Op, args(a.Inverse.Args))
	} else {
		err = wr.Undo(a.Changes)
	}
	if err != nil {
		writeStatus(w, http.StatusConflict, sentence(err.Error()))
		return
	}
	s.settle(wr)
	activityMu.Lock()
	all = s.loadActivity()
	if j := slices.IndexFunc(all, func(x Activity) bool { return x.ID == req.ID }); j >= 0 {
		all[j].Undone = true
		_ = s.saveActivity(all)
	}
	activityMu.Unlock()
	s.logActivity(Activity{Who: "you", Title: "Undid: " + a.Title, Detail: a.Detail, Changes: wr.Changes})
	s.Refresh(r.Context(), a.Inverse != nil)
	writeStatus(w, http.StatusOK, "Undone.")
}

// settle tags the new content of every file an operation wrote as a version made by you, so the
// watcher doesn't mistake cca's own writes for Claude's.
func (s *Server) settle(wr *write.Writer) {
	seen := map[string]bool{}
	for _, c := range wr.Changes {
		if !seen[c.Path] {
			seen[c.Path] = true
			_, _, _ = wr.Record(c.Path, "you")
		}
	}
}

// recordClaude logs memory files that changed on disk without cca: Claude's own edits.
func (s *Server) recordClaude(paths map[string]bool) {
	s.writing.Lock()
	defer s.writing.Unlock()
	wr := write.New(s.Home)
	var changes []write.Change
	var names []string
	for p := range paths {
		if !strings.HasSuffix(p, ".md") || filepath.Base(filepath.Dir(p)) != "memory" {
			continue
		}
		prev, cur, err := wr.Record(p, "claude")
		if err != nil || cur == "" {
			continue
		}
		changes = append(changes, write.Change{Path: p, Before: prev})
		if filepath.Base(p) != "MEMORY.md" {
			names = append(names, strings.TrimSuffix(filepath.Base(p), ".md"))
		}
	}
	if len(names) == 0 {
		return // only the index changed, or nothing new
	}
	slices.Sort(names)
	title := "Claude updated a memory"
	if len(names) > 1 {
		title = "Claude updated " + strconv.Itoa(len(names)) + " memories"
	}
	s.logActivity(Activity{Who: "claude", Title: title, Detail: strings.Join(names, ", "), Changes: changes})
}

// baseline keeps the current text of every memory as its first version, so Claude's first edit
// to any of them can be undone. Files that already have versions are skipped.
func (s *Server) baseline() {
	wr := write.New(s.Home)
	s.mu.RLock()
	var paths []string
	for _, e := range s.state.Entries {
		if e.Kind == entry.Memory || e.Kind == entry.MemoryIndex {
			paths = append(paths, e.Path)
		}
	}
	s.mu.RUnlock()
	for _, p := range paths {
		if len(wr.Versions(p)) == 0 {
			_, _, _ = wr.Record(p, "original")
		}
	}
}

// handleDecide accepts (runs, undoably) or dismisses one of Claude's proposals.
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string
		Accept bool
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeStatus(w, http.StatusBadRequest, "Bad request.")
		return
	}
	dir := write.DataDir(s.Home)
	if !req.Accept {
		if _, err := proposal.Decide(dir, req.ID, proposal.Pending, proposal.Dismissed); err != nil {
			writeStatus(w, http.StatusBadRequest, sentence(err.Error()))
			return
		}
		s.Refresh(r.Context(), false)
		writeStatus(w, http.StatusOK, "Dismissed.")
		return
	}
	if s.ReadOnly {
		writeStatus(w, http.StatusForbidden, "Read-only mode: start cca without --read-only to make changes.")
		return
	}
	s.writing.Lock()
	defer s.writing.Unlock()
	if all := proposal.Load(dir); !slices.ContainsFunc(all, func(p proposal.Proposal) bool { return p.ID == req.ID && slices.Contains(proposal.Ops, p.Op) }) {
		writeStatus(w, http.StatusBadRequest, "Claude can't suggest that kind of change.") // e.g. a line written into proposals.jsonl by hand
		return
	}
	p, err := proposal.Decide(dir, req.ID, proposal.Pending, proposal.Accepted) // claim it first
	if err != nil {
		writeStatus(w, http.StatusBadRequest, sentence(err.Error()))
		return
	}
	wr := write.New(s.Home)
	var msg string
	var inverse *actRequest
	// From Claude, memories only ever go to Claude Code's own folders: never into a repo (an
	// agent-memory folder there gets committed), which would publish private notes.
	if d, _ := p.Args["dir"].(string); d != "" && !strings.HasPrefix(filepath.Clean(d)+"/", scan.ConfigDir(s.Home)+"/") {
		err = errUser{"Claude can only suggest memory folders inside Claude Code's own config, not in a repo."}
	} else {
		msg, inverse, err = s.do(r.Context(), wr, p.Op, args(p.Args))
	}
	if err != nil {
		_, _ = proposal.Decide(dir, p.ID, proposal.Accepted, proposal.Pending) // still waiting in Review
		writeStatus(w, http.StatusConflict, sentence(err.Error()))
		return
	}
	s.settle(wr)
	act := s.logActivity(Activity{Who: "you", Title: "Accepted Claude’s suggestion: " + titleFor(p.Op, args(p.Args)),
		Detail: p.Reason, Changes: wr.Changes, Inverse: inverse})
	s.Refresh(r.Context(), strings.HasPrefix(p.Op, "plugin-"))
	writeJSON(w, map[string]any{"message": msg, "activity": act.ID, "canUndo": act.CanUndo()})
}
