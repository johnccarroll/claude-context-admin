// Package proposal stores changes Claude suggests through the MCP server. They wait in Review
// until the user accepts (the app runs the change) or dismisses them. The MCP process writes;
// the app reads, so the file is the hand-off.
package proposal

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/write"
)

// Ops are the changes Claude may suggest. Accepting runs only these: adding MCP servers or
// plugins (which run commands) and writing skills stay with the user, in the app.
var Ops = []string{"memory-save", "memory-create", "memory-trash", "memory-promote", "relink", "unlink",
	"fix-frontmatter", "merge-global", "remove-hooks", "quiet-caps", "convert-to-agents",
	"plugin-enable", "plugin-disable", "plugin-uninstall", "mcp-remove", "bulk", "project-relocate"}

// MaxPending caps suggestions waiting in Review, so a looping or prompt-injected Claude can't
// flood it (each can be up to 256 KB, and the file is reread on every refresh).
const MaxPending = 100

// ErrTooMany means Review already holds MaxPending suggestions.
var ErrTooMany = errors.New("Review already has 100 suggestions waiting: ask the user to go through them first")

// ErrHandled means the proposal was already accepted or dismissed (or doesn't exist).
var ErrHandled = errors.New("that suggestion was already handled")

// Status of a proposal.
const (
	Pending   = "pending"
	Accepted  = "accepted"
	Dismissed = "dismissed"
)

// Proposal is one suggested change.
type Proposal struct {
	ID     string         `json:"id"`
	At     time.Time      `json:"at"`
	Op     string         `json:"op"`
	Args   map[string]any `json:"args"`
	Reason string         `json:"reason"`
	Status string         `json:"status"`
}

func path(dir string) string { return filepath.Join(dir, "proposals.jsonl") }

// Load returns every proposal, oldest first.
func Load(dir string) []Proposal {
	f, err := os.Open(path(dir))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Proposal
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var p Proposal
		if json.Unmarshal(sc.Bytes(), &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// locked runs fn while holding an exclusive lock on the proposals file. Each `cca mcp` process
// and the app all write it, so every read-modify-write goes through here.
func locked(dir string, fn func() error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "proposals.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing releases it anyway
	return fn()
}

// Add stores a new pending proposal and returns it.
func Add(dir, op string, args map[string]any, reason string) (Proposal, error) {
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	p := Proposal{ID: hex.EncodeToString(id), At: time.Now().UTC(), Op: op, Args: args, Reason: reason, Status: Pending}
	return p, locked(dir, func() error {
		all := Load(dir)
		pending := 0
		for _, q := range all {
			if q.Status == Pending {
				pending++
			}
		}
		if pending >= MaxPending {
			return ErrTooMany
		}
		return save(dir, append(all, p))
	})
}

// Decide moves a proposal from one status to another, and fails with ErrHandled if it isn't in
// the from status. Claiming a pending proposal this way means it can only ever be applied once.
func Decide(dir, id, from, to string) (Proposal, error) {
	var out Proposal
	err := locked(dir, func() error {
		all := Load(dir)
		for i := range all {
			if all[i].ID == id && all[i].Status == from {
				all[i].Status = to
				out = all[i]
				return save(dir, all)
			}
		}
		return ErrHandled
	})
	return out, err
}

// save keeps pending proposals and the latest 200 decided ones, written atomically.
func save(dir string, all []Proposal) error {
	var keep []Proposal
	decided := 0
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Status == Pending || decided < 200 {
			keep = append([]Proposal{all[i]}, keep...)
			if all[i].Status != Pending {
				decided++
			}
		}
	}
	var b strings.Builder
	for _, p := range keep {
		j, _ := json.Marshal(p)
		b.Write(j)
		b.WriteByte('\n')
	}
	return write.Atomic(path(dir), []byte(b.String()), 0o600)
}
