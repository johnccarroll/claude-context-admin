package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/johnccarroll/claude-context-admin/internal/write"
)

// ProjectPrefs are display-only choices; they never change Claude Code's files.
type ProjectPrefs struct {
	Alias    string `json:"alias,omitempty"`
	Favorite bool   `json:"favorite,omitempty"`
	Hidden   bool   `json:"hidden,omitempty"`
}

// Prefs is the app's own settings file.
type Prefs struct {
	Projects map[string]ProjectPrefs `json:"projects"`
	Order    []string                `json:"order"` // project keys in the user's order
	// Dismissed are Review cards the user chose to keep as they are ("Keep", "Not now").
	Dismissed []string `json:"dismissed,omitempty"`
}

var prefsMu sync.Mutex

// prefsPath is per user, or inside the scanned home when --home points elsewhere (tests, sandboxes).
func (s *Server) prefsPath() string { return filepath.Join(write.DataDir(s.Home), "prefs.json") }

func (s *Server) loadPrefs() Prefs {
	p := Prefs{Projects: map[string]ProjectPrefs{}, Order: []string{}}
	if b, err := os.ReadFile(s.prefsPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if p.Projects == nil {
		p.Projects = map[string]ProjectPrefs{}
	}
	if p.Order == nil {
		p.Order = []string{}
	}
	return p
}

func (s *Server) handleGetPrefs(w http.ResponseWriter, _ *http.Request) {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	writeJSON(w, s.loadPrefs())
}

// handlePutPrefs replaces the prefs. Allowed in read-only mode: it is the app's file, not Claude's.
func (s *Server) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	var p Prefs
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") ||
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&p) != nil {
		writeStatus(w, http.StatusBadRequest, "Those preferences couldn't be read.")
		return
	}
	if len(p.Dismissed) > 1000 { // oldest first; a cap keeps the file small
		p.Dismissed = p.Dismissed[len(p.Dismissed)-1000:]
	}
	for k, v := range p.Projects {
		if len(v.Alias) > 80 {
			v.Alias = v.Alias[:80]
			p.Projects[k] = v
		}
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	prefsMu.Lock()
	defer prefsMu.Unlock()
	if err := write.Atomic(s.prefsPath(), b, 0o600); err != nil {
		writeStatus(w, http.StatusInternalServerError, "Preferences couldn't be saved: "+err.Error())
		return
	}
	writeJSON(w, p)
}
