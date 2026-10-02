// Package server serves the UI and its JSON API on 127.0.0.1.
//
// Security model for a local app that can change files:
//   - binds to loopback only;
//   - a random per-launch token must be presented once (?t=...), then lives in an HttpOnly,
//     SameSite=Strict cookie, so other sites and other local users can't call the API;
//   - the Host header must be the loopback address we bound, which blocks DNS rebinding;
//   - no CORS headers are ever sent.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/audit"
	"github.com/johnccarroll/claude-context-admin/internal/doctor"
	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/install"
	"github.com/johnccarroll/claude-context-admin/internal/loads"
	"github.com/johnccarroll/claude-context-admin/internal/proposal"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/usage"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

const cookiePrefix = "cca_token_"

// State is what the UI renders. It is rebuilt whenever watched files change.
type State struct {
	Home      string              `json:"home"`
	Projects  []scan.Project      `json:"projects"`
	Entries   []entry.Entry       `json:"entries"`
	Usage     usage.Usage         `json:"usage"`
	Report    audit.Report        `json:"report"`
	Proposals []proposal.Proposal `json:"proposals"` // Claude's pending suggestions (from cca mcp)
	Health    []doctor.Check      `json:"health"`    // doctor checks that aren't ok: features degraded on this Claude Code version
	ReadOnly  bool                `json:"readOnly"`
	OS        string              `json:"os"` // runtime.GOOS, for words like "Finder"
	// Claude Code facts the UI shows, owned here so they change in one place.
	GlobalMemoryDir string `json:"globalMemoryDir"` // where Everywhere memories go
	IndexMaxLines   int    `json:"indexMaxLines"`   // how much of MEMORY.md loads
	IndexMaxBytes   int    `json:"indexMaxBytes"`
}

// Server holds the latest state and the open live-reload streams.
type Server struct {
	Home     string
	ReadOnly bool
	Static   fs.FS                   // the built UI
	Run      scan.Runner             // read-only CLI calls during scans
	CLI      CLI                     // CLI calls that change config; nil uses the real claude
	Reveal   func(path string) error // shows a path in Finder or the file manager; nil disables it

	// Dev mode (scripts/dev.sh): DevDir is the UI folder served from disk; open pages reload when
	// it changes or the server restarts. FixedToken keeps the sign-in link stable across restarts.
	DevDir     string
	FixedToken string

	plugins scan.PluginCache // last `claude plugin list`, reused while plugin state is unchanged
	token   string
	addr    string

	mu        sync.RWMutex
	state     *State
	inv       *scan.Inventory
	costs     map[string]map[string]any  // plugin id -> cost meta, reused between scans
	reloaders map[chan struct{}]struct{} // dev mode: pages to reload after a UI rebuild
	boot      string                     // changes every start, so dev pages reload after a restart
	clients   map[chan struct{}]struct{}
	sum       [32]byte // fingerprint of the last entries, so no-op rewrites don't reload the UI
	health    []doctor.Check

	// writing is held while a change runs and is recorded, and while the watcher attributes
	// changes it saw to Claude, so cca's own writes are never logged as Claude's.
	writing sync.Mutex
}

// Listen binds a loopback port (0 picks a free one) and returns the URL to open.
// cookieName is per port: two cca instances (say a sandbox and the real one) don't sign each
// other out.
func (s *Server) cookieName() string { return cookiePrefix + portOf(s.addr) }

func (s *Server) Listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, "", err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	s.token = hex.EncodeToString(b)
	if s.FixedToken != "" {
		s.token = s.FixedToken
	}
	s.boot = hex.EncodeToString(b[:6])
	s.addr = ln.Addr().String()
	s.clients = map[chan struct{}]struct{}{}
	s.reloaders = map[chan struct{}]struct{}{}
	return ln, "http://" + s.addr + "/?t=" + s.token, nil
}

// Serve rebuilds state, starts watching for changes and serves until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.Refresh(ctx, true)
	go s.watch(ctx)
	if !s.ReadOnly {
		go s.baseline()
	}
	go s.checkHealth(ctx)
	if s.DevDir != "" {
		go s.devWatch(ctx)
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Refresh rescans. Plugin token costs come from slow CLI calls, so they're only re-read when
// withCosts is true (startup and plugin changes) and otherwise carried over by plugin id.
func (s *Server) Refresh(ctx context.Context, withCosts bool) {
	inv := (&scan.Scanner{Home: s.Home, Run: s.Run, Costs: withCosts, Plugins: &s.plugins}).Scan(ctx)
	s.mu.Lock()
	if withCosts {
		s.costs = map[string]map[string]any{}
	}
	for i := range inv.Entries {
		e := &inv.Entries[i]
		if e.Kind != entry.Plugin {
			continue
		}
		id, _ := e.Meta["id"].(string)
		if withCosts {
			s.costs[id] = map[string]any{"components": e.Meta["components"], "alwaysOnTokens": e.Meta["alwaysOnTokens"],
				"perComponent": e.Meta["perComponent"], "description": e.Description}
		} else if c, ok := s.costs[id]; ok {
			for k, v := range c {
				if k == "description" {
					e.Description, _ = v.(string)
				} else {
					e.Meta[k] = v
				}
			}
		}
	}
	s.mu.Unlock()
	now := time.Now()
	u := usage.Scan(filepath.Join(scan.ConfigDir(s.Home), "projects"), now.Add(-audit.StaleAfter), CachePath(s.Home))
	pending := []proposal.Proposal{}
	for _, p := range proposal.Load(write.DataDir(s.Home)) {
		if p.Status == proposal.Pending {
			pending = append(pending, p)
		}
	}
	s.mu.RLock()
	health := s.health
	s.mu.RUnlock()
	st := &State{Home: s.Home, Projects: inv.Projects, Entries: inv.Entries, GlobalMemoryDir: inv.GlobalMemoryDir,
		IndexMaxLines: scan.IndexMaxLines, IndexMaxBytes: scan.IndexMaxBytes, Usage: u, Proposals: pending, Health: health,
		Report: audit.Run(inv, u, now), ReadOnly: s.ReadOnly, OS: runtime.GOOS}
	sum := fingerprint(inv.Entries, pending)
	s.mu.Lock()
	changed := sum != s.sum
	s.state, s.inv, s.sum = st, inv, sum
	for c := range s.clients {
		if !changed {
			break
		}
		select {
		case c <- struct{}{}:
		default: // client already has a pending notice
		}
	}
	s.mu.Unlock()
}

// checkHealth runs cca doctor once in the background and shows any degraded features in the UI.
func (s *Server) checkHealth(ctx context.Context) {
	var bad []doctor.Check
	for _, c := range doctor.Run(ctx, s.Home, s.Run) {
		if c.Status != doctor.OK {
			bad = append(bad, c)
		}
	}
	s.mu.Lock()
	s.health = bad
	s.sum = [32]byte{} // force the next refresh to notify the UI
	s.mu.Unlock()
	s.Refresh(ctx, false)
}

// Handler is the full HTTP surface; exported for tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/budget", s.handleBudget)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/file", s.handleFile)
	mux.HandleFunc("POST /api/act", s.handleAct)
	mux.HandleFunc("POST /api/reveal", s.handleReveal)
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("POST /api/undo", s.handleUndo)
	mux.HandleFunc("GET /api/versions", s.handleVersions)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/proposals/decide", s.handleDecide)
	mux.HandleFunc("GET /api/preview/quiet-caps", s.handlePreviewCaps)
	mux.HandleFunc("GET /api/prefs", s.handleGetPrefs)
	mux.HandleFunc("PUT /api/prefs", s.handlePutPrefs)
	mux.HandleFunc("POST /api/install/parse", s.handleInstallParse)
	mux.HandleFunc("POST /api/rescan", s.handleRescan)
	if s.Static != nil {
		mux.Handle("GET /", http.FileServerFS(s.Static))
	}
	// Rejects cross-origin writes (Sec-Fetch-Site / Origin), including other localhost ports,
	// which SameSite cookies don't separate.
	h := s.guard(http.NewCrossOriginProtection().Handler(mux))
	if s.DevDir != "" {
		return devLog(h)
	}
	return h
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.addr && r.Host != "localhost:"+portOf(s.addr) {
			http.Error(w, "wrong host", http.StatusMisdirectedRequest)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && s.tokenOK(t) {
			http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.token, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther) // drop the token from the address bar
			return
		}
		c, err := r.Cookie(s.cookieName())
		if err != nil || !s.tokenOK(c.Value) {
			http.Error(w, "Open the link printed by `cca` to sign in.", http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"font-src 'self' data:; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	st := s.state
	s.mu.RUnlock()
	writeJSON(w, st)
}

func (s *Server) handleBudget(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("project")
	s.mu.RLock()
	inv := s.inv
	s.mu.RUnlock()
	writeJSON(w, map[string]any{"budget": loads.Compute(inv, p), "shadows": loads.Shadows(inv, p)})
}

// handleEvents streams a "changed" event whenever state is rebuilt.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	c := make(chan struct{}, 1)
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.clients, c); s.mu.Unlock() }()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	_, _ = w.Write([]byte(": connected\n\n"))
	rc := make(chan struct{}, 1)
	if s.DevDir != "" { // dev: a page that sees a new boot id, or a UI rebuild, reloads
		_, _ = fmt.Fprintf(w, "event: boot\ndata: %s\n\n", s.boot)
		s.mu.Lock()
		s.reloaders[rc] = struct{}{}
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.reloaders, rc); s.mu.Unlock() }()
	}
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-rc:
			_, _ = w.Write([]byte("event: reload\ndata: {}\n\n"))
		case <-c:
			_, _ = w.Write([]byte("event: changed\ndata: {}\n\n"))
		case <-ping.C:
			_, _ = w.Write([]byte(": ping\n\n"))
		}
		fl.Flush()
	}
}

func fingerprint(es []entry.Entry, ps []proposal.Proposal) [32]byte {
	b, _ := json.Marshal([]any{es, ps})
	return sha256.Sum256(b)
}

// handleVersions lists a readable file's saved versions, newest first, with their text.
func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.readable(p) {
		http.Error(w, "not a readable entry", http.StatusNotFound)
		return
	}
	type ver struct {
		write.Version
		Content string `json:"content"`
	}
	out := []ver{}
	for _, v := range write.New(s.Home).Versions(p) {
		b, err := os.ReadFile(v.Path)
		if err == nil {
			out = append(out, ver{v, string(b)})
		}
	}
	writeJSON(w, out)
}

// handleSearch finds q (case-insensitive) in the text of every readable file and returns where.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	type hit struct {
		Path    string     `json:"path"`
		Kind    entry.Kind `json:"kind"`
		Snippet string     `json:"snippet"`
	}
	out := []hit{}
	if len(q) < 2 {
		writeJSON(w, out)
		return
	}
	s.mu.RLock()
	es := s.state.Entries
	s.mu.RUnlock()
	for _, e := range es {
		if !readable[e.Kind] || len(out) >= 60 {
			continue
		}
		b, err := scan.ReadText(e.Path)
		if err != nil || !strings.Contains(strings.ToLower(string(b)), q) {
			continue
		}
		out = append(out, hit{e.Path, e.Kind, scan.Snippet(string(b), q)})
	}
	writeJSON(w, out)
}

// handlePreviewCaps shows what "quiet-caps" would change, without writing.
func (s *Server) handlePreviewCaps(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.readable(p) {
		http.Error(w, "not a readable entry", http.StatusNotFound)
		return
	}
	b, err := scan.ReadText(p)
	if err != nil {
		http.Error(w, "could not read file", http.StatusNotFound)
		return
	}
	after, n := audit.QuietCaps(string(b))
	writeJSON(w, map[string]any{"before": string(b), "after": after, "changes": n})
}

func (s *Server) readable(p string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.state.Entries {
		if e.Path == p && readable[e.Kind] {
			return true
		}
	}
	return false
}

// readable kinds are plain markdown; settings, MCP config and plugin manifests are never served.
var readable = map[entry.Kind]bool{entry.Memory: true, entry.MemoryIndex: true, entry.Instructions: true,
	entry.Rule: true, entry.Skill: true, entry.Command: true, entry.Agent: true}

// handleFile returns a file's text, but only for a markdown entry already in the inventory, so the
// API can't be used to read arbitrary paths.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	s.mu.RLock()
	var e *entry.Entry
	for i := range s.state.Entries {
		if s.state.Entries[i].Path == p && readable[s.state.Entries[i].Kind] {
			e = &s.state.Entries[i]
			break
		}
	}
	s.mu.RUnlock()
	if e == nil {
		http.Error(w, "not a readable entry", http.StatusNotFound)
		return
	}
	b, err := scan.ReadText(p)
	if err != nil {
		http.Error(w, "could not read file", http.StatusNotFound)
		return
	}
	_, body, _ := scan.SplitHeader(string(b)) // the text after the header, split by the same code that writes it back
	writeJSON(w, map[string]string{"path": p, "content": string(b), "body": body, "modified": e.Modified})
}

type actRequest struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return errors.New("expected application/json: " + ct) // a plain HTML form can't send JSON
	}
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v)
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func portOf(addr string) string {
	_, p, _ := net.SplitHostPort(addr)
	return p
}

// handleInstallParse previews a paste (an MCP snippet, `claude mcp add`, `/plugin` commands or a
// SKILL.md). It changes nothing; applying goes through /api/act.
func (s *Server) handleInstallParse(w http.ResponseWriter, r *http.Request) {
	var req struct{ Text string }
	if err := decodeJSON(w, r, &req); err != nil {
		writeStatus(w, http.StatusBadRequest, "Bad request.")
		return
	}
	p, err := install.Parse(req.Text)
	if err != nil {
		writeStatus(w, http.StatusBadRequest, sentence(err.Error()))
		return
	}
	lits := map[string][]string{}
	for _, sv := range p.Servers {
		if l := sv.Literals(); len(l) > 0 {
			lits[sv.Name] = l
		}
	}
	writeJSON(w, map[string]any{"plan": p, "literals": lits})
}

// devWatch tells open pages to reload when the UI folder changes (a rebuild). Dev mode only.
func (s *Server) devWatch(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer w.Close()
	if w.Add(s.DevDir) != nil {
		return
	}
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.Events:
			pending = time.After(150 * time.Millisecond) // a rebuild writes several files
		case <-pending:
			s.mu.RLock()
			for rc := range s.reloaders {
				select {
				case rc <- struct{}{}:
				default:
				}
			}
			s.mu.RUnlock()
		case <-w.Errors:
		}
	}
}

// devLog prints one line per request in dev mode: method, path, status, time. Never the query
// string (it carries the sign-in token) or bodies (they carry memory text and keys).
func devLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/events" { // a long-lived stream, not a request worth timing
			next.ServeHTTP(w, r)
			return
		}
		t := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		fmt.Fprintf(os.Stderr, "%s %-6s %-28s %d %s\n", t.Format("15:04:05"), r.Method, r.URL.Path, rec.code, time.Since(t).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) { r.code = code; r.ResponseWriter.WriteHeader(code) }

// handleRescan re-reads everything now (Review's "Check again"), including plugin costs.
func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	s.Refresh(r.Context(), true)
	writeStatus(w, http.StatusOK, "Checked.")
}
