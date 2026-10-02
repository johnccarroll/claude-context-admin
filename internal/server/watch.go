package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/johnccarroll/claude-context-admin/internal/audit"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

// CachePath is where usage stats are cached between scans; tests and --home runs skip it.
func CachePath(home string) string {
	if h, _ := os.UserHomeDir(); h != home {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "claude-context-admin", "usage.json")
}

// noisy files change constantly and never affect what we show.
func noisy(name string) bool {
	return strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".tmp") ||
		strings.HasPrefix(filepath.Base(name), ".consolidate-lock") || strings.Contains(name, "/.git/")
}

// watch rescans when anything we show changes. It watches exactly the directories the inventory
// touches (plus ~/.claude and the plugin dir), debounces bursts, and re-reads plugin costs only
// when plugins change. ~/.claude.json is polled rather than watched: watching the home folder
// itself opens every item in it on macOS (Desktop, Documents, Downloads), which can bring privacy
// prompts and endpoint-security noise.
func (s *Server) watch(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer w.Close()
	watched := map[string]bool{}
	add := func() {
		s.mu.RLock()
		dirs := map[string]bool{scan.ConfigDir(s.Home): true, filepath.Join(scan.ConfigDir(s.Home), "plugins"): true,
			write.DataDir(s.Home): true} // data dir for Claude's proposals
		for _, e := range s.state.Entries {
			if e.Path != "" {
				dirs[filepath.Dir(e.Path)] = true
			}
		}
		for _, p := range s.state.Projects {
			if p.Exists && !p.Worktree {
				dirs[filepath.Join(p.Path, ".claude")] = true
			}
		}
		s.mu.RUnlock()
		for d := range dirs {
			// Never the home folder (an entry in ~/.claude.json lives there) or a private one: on
			// macOS watching a folder opens every item in it (Desktop, Documents…).
			if d == s.Home || d == filepath.Dir(scan.GlobalConfig(s.Home)) || audit.Private(d, s.Home) {
				continue
			}
			if !watched[d] && w.Add(d) == nil {
				watched[d] = true
			}
		}
	}
	add()
	global := scan.GlobalConfig(s.Home)
	// Claude Code rewrites ~/.claude.json all the time (session stats, project history). A rescan
	// follows only when the MCP servers in it change.
	stamp := func() string {
		fi, err := os.Stat(global)
		if err != nil {
			return ""
		}
		return fi.ModTime().String() + "/" + strconv.FormatInt(fi.Size(), 10)
	}
	last, lastMCP := stamp(), scan.MCPStamp(s.Home)
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	pending, costs := false, false
	changed := map[string]bool{} // memory files touched since the last rescan
	fire := make(chan struct{}, 1)
	seen := func(name string) {
		if noisy(name) {
			return
		}
		if strings.HasPrefix(name, filepath.Join(scan.ConfigDir(s.Home), "plugins")) {
			costs = true
		}
		changed[name] = true
		if !pending {
			pending = true
			time.AfterFunc(400*time.Millisecond, func() { fire <- struct{}{} })
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			if now := stamp(); now != last {
				last = now
				if m := scan.MCPStamp(s.Home); m != lastMCP {
					lastMCP = m
					seen(global)
				}
			}
		case ev := <-w.Events:
			seen(ev.Name)
		case <-fire:
			pending = false
			c := costs
			costs = false
			if !s.ReadOnly {
				s.recordClaude(changed)
			}
			changed = map[string]bool{}
			s.Refresh(ctx, c)
			add() // new skills, memory dirs or repos
		case <-w.Errors:
		}
	}
}
