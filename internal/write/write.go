// Package write is the single path for every file change cca makes.
//
// Each change snapshots the previous version into the history dir first, then writes atomically
// (temp file + rename). Deletions go to the Trash, never unlink. Memory writes are refused while
// Claude Code's memory consolidation holds its lock.
package write

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// ErrLocked means Claude Code is consolidating memories in that folder right now.
var ErrLocked = errors.New("Claude is tidying memories in this folder right now; try again in a minute")

// ErrChanged means the file changed on disk after the user opened it.
var ErrChanged = errors.New("this file changed since you opened it; reload to see the latest version")

// lockTTL matches Claude Code's own staleness window for .consolidate-lock. The file's contents
// are undocumented and have no changelog history, so cca treats it as opaque and only reads its mtime.
const lockTTL = time.Hour

// keepSnapshots is how many past versions of one file history keeps.
const keepSnapshots = 50

// Writer performs changes for one home directory.
type Writer struct {
	Home    string
	History string // where snapshots go
	Trash   string // where deleted files go (~/.Trash on macOS)
	// TrashInfo writes the freedesktop.org .trashinfo next to each deleted file, so Linux
	// desktops list it in their Trash and can put it back.
	TrashInfo bool
	Now       func() time.Time
	// Changes lists every file this Writer touched, in order, so the operation can be undone.
	Changes []Change
	// Confine, when set, refuses any write whose real (symlink-resolved) location is outside it.
	// Sandboxes (--home) set it so a symlink in the copy can never reach the real config.
	Confine string
}

// ErrOutside means a change would land outside a --home sandbox (usually through a symlink).
var ErrOutside = errors.New("refused: that file resolves outside this sandbox home (through a symlink)")

// inside reports whether path's real location (or its nearest existing parent's) is under Confine.
func (w *Writer) inside(path string) error {
	if w.Confine == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(w.Confine)
	if err != nil {
		return err
	}
	p := path
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			rel := strings.TrimPrefix(path, p) // part that doesn't exist yet
			if real := r + rel; real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
				return ErrOutside
			}
			return nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ErrOutside
		}
		p = parent
	}
}

// DataDir is where cca keeps its own files (history, activity, proposals, prefs): the platform's
// config dir for the real home, or inside a sandbox home.
func DataDir(home string) string {
	if h, _ := os.UserHomeDir(); h == home {
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, "claude-context-admin")
		}
	}
	return filepath.Join(home, ".claude-context-admin")
}

// New returns a Writer for home. For the real home it uses the desktop's Trash (~/.Trash on
// macOS, the freedesktop.org Trash on Linux); any other home is a sandbox with its own .Trash,
// confined so no write can escape it.
func New(home string) *Writer {
	w := &Writer{Home: home, Now: time.Now, Confine: home,
		History: filepath.Join(DataDir(home), "history"),
		Trash:   filepath.Join(home, ".Trash")}
	if h, _ := os.UserHomeDir(); h == home {
		w.Confine = ""
		if runtime.GOOS == "linux" {
			data := os.Getenv("XDG_DATA_HOME")
			if !filepath.IsAbs(data) {
				data = filepath.Join(home, ".local", "share")
			}
			w.Trash, w.TrashInfo = filepath.Join(data, "Trash", "files"), true
		}
	}
	return w
}

// trashInfo is the freedesktop.org record for a file moved to dst from path.
func (w *Writer) trashInfo(dst string) string {
	return filepath.Join(filepath.Dir(w.Trash), "info", filepath.Base(dst)+".trashinfo")
}

// CheckLock refuses writes in a memory folder while consolidation may be running.
func CheckLock(dir string) error {
	if fi, err := os.Stat(filepath.Join(dir, ".consolidate-lock")); err == nil && time.Since(fi.ModTime()) < lockTTL {
		return ErrLocked
	}
	return nil
}

// CheckUnchanged compares the file's mtime with what the client saw (RFC 3339, empty to skip).
func CheckUnchanged(path, seen string) error {
	if seen == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil // new file
	}
	t, err := time.Parse(time.RFC3339, seen)
	if err != nil {
		return nil
	}
	if fi.ModTime().UTC().Truncate(time.Second).After(t.UTC()) {
		return ErrChanged
	}
	return nil
}

// File writes b to path, keeping the old content as a version and recording the change.
func (w *Writer) File(path string, b []byte) error {
	if err := w.inside(path); err != nil {
		return err
	}
	if _, err := readCapped(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err // over the size the scan and editor read: replacing it would cut off its tail
	}
	if err := w.linkOK(path); err != nil {
		return err
	}
	before, err := w.snapshot(path, "original") // author unknown; settle() tags the new content
	if err != nil {
		return err
	}
	if err := Atomic(path, b, 0o644); err != nil {
		return err
	}
	w.Changes = append(w.Changes, Change{Path: path, Before: before, After: digest(b)})
	return nil
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// unchanged reports whether path still holds what change c wrote, so undoing it can't wipe a
// later edit (a deny rule added by hand, say). Records from before this check pass.
func unchanged(c Change) bool {
	if c.After == "" {
		return true
	}
	b, err := os.ReadFile(c.Path)
	return err == nil && digest(b) == c.After
}

// Remove moves path to the Trash after snapshotting it, and returns where it went.
func (w *Writer) Remove(path string) (string, error) {
	if err := w.inside(path); err != nil {
		return "", err
	}
	before, err := w.snapshot(path, "original")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(w.Trash, 0o700); err != nil {
		return "", err
	}
	name := filepath.Base(path)
	if filepath.Base(filepath.Dir(path)) != "" && strings.EqualFold(name, "SKILL.md") {
		name = filepath.Base(filepath.Dir(path)) + "-SKILL.md" // keep skills recognisable in the Trash
	}
	dst := filepath.Join(w.Trash, name)
	for i := 2; exists(dst) || (w.TrashInfo && exists(w.trashInfo(dst))); i++ {
		ext := filepath.Ext(name)
		dst = filepath.Join(w.Trash, fmt.Sprintf("%s %d%s", strings.TrimSuffix(name, ext), i, ext))
	}
	if w.TrashInfo { // written first, as the spec asks, so the desktop never sees an orphan
		info := w.trashInfo(dst)
		_ = os.MkdirAll(filepath.Dir(info), 0o700)
		rec := "[Trash Info]\nPath=" + (&url.URL{Path: path}).EscapedPath() + "\nDeletionDate=" + w.Now().Format("2006-01-02T15:04:05") + "\n"
		if err := os.WriteFile(info, []byte(rec), 0o600); err != nil {
			return "", err
		}
	}
	if err := os.Rename(path, dst); err != nil {
		if w.TrashInfo {
			_ = os.Remove(w.trashInfo(dst))
		}
		return "", err
	}
	w.Changes = append(w.Changes, Change{Path: path, Before: before, Trashed: dst})
	return dst, nil
}

// Change is one file an operation touched, with enough to undo it.
type Change struct {
	Path    string `json:"path"`
	Before  string `json:"before,omitempty"`  // snapshot of the previous content; "" means the file was new
	Trashed string `json:"trashed,omitempty"` // where Remove put it
	After   string `json:"after,omitempty"`   // sha256 of what the change wrote
}

// Version is one saved state of a file.
type Version struct {
	Path string    `json:"path"` // snapshot file
	At   time.Time `json:"at"`
	Who  string    `json:"who"` // "you" (a change made in cca), "claude" (seen on disk) or "original"
}

const stampFmt = "20060102T150405.000000000Z"

// Versions lists saved versions of path, newest first.
func (w *Writer) Versions(path string) []Version {
	dir := w.snapDir(path)
	des, _ := os.ReadDir(dir)
	var out []Version
	for _, de := range des {
		stamp, who, _ := strings.Cut(strings.TrimSuffix(de.Name(), ".snap"), "~")
		if t, err := time.Parse(stampFmt, stamp); err == nil {
			if who == "" {
				who = "you"
			}
			out = append(out, Version{Path: filepath.Join(dir, de.Name()), At: t, Who: who})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Restore writes a saved version back; the current content is itself kept as a version.
func (w *Writer) Restore(path, version string) error {
	// Only a file directly inside this file's own version folder: no "..", no other folders.
	if filepath.Dir(filepath.Clean(version)) != filepath.Clean(w.snapDir(path)) || !strings.HasSuffix(version, ".snap") {
		return errors.New("that version doesn't belong to this file")
	}
	b, err := os.ReadFile(filepath.Clean(version))
	if err != nil {
		return err
	}
	return w.File(path, fromHistory(path, b))
}

// Record saves path's current content as a version made by who (e.g. Claude editing a memory),
// unless it matches the latest version. It returns the previous and new version paths; both are
// empty when nothing changed.
func (w *Writer) Record(path, who string) (prev, cur string, err error) {
	if v := w.Versions(path); len(v) > 0 {
		prev = v[0].Path
	}
	cur, err = w.snapshot(path, who)
	if cur == prev {
		return "", "", err
	}
	return prev, cur, err
}

// Undo reverses a change set, newest change first. The undo is itself recorded in w.Changes.
func (w *Writer) Undo(changes []Change) error {
	// All or nothing: check every file first, so a later edit to one stops the whole undo.
	last := map[string]Change{} // a change can write a file twice; what matters is the final write
	for _, c := range changes {
		// The record could have been edited on disk: undo restores only from this file's own
		// versions, and brings back only what sits in the Trash.
		if c.Before != "" && filepath.Dir(filepath.Clean(c.Before)) != w.snapDir(c.Path) {
			return fmt.Errorf("the record for %s doesn't point at its saved versions", filepath.Base(c.Path))
		}
		if c.Trashed != "" && filepath.Dir(filepath.Clean(c.Trashed)) != filepath.Clean(w.Trash) {
			return fmt.Errorf("the record for %s doesn't point into the Trash", filepath.Base(c.Path))
		}
		last[c.Path] = c
	}
	for _, c := range last {
		if c.Trashed == "" && exists(c.Path) && !unchanged(c) {
			return fmt.Errorf("%s changed since, so undoing would lose that: restore an earlier version from its history instead", filepath.Base(c.Path))
		}
	}
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		switch {
		case c.Trashed != "":
			if exists(c.Path) {
				return fmt.Errorf("%s exists again, so it can't be brought back from the Trash", filepath.Base(c.Path))
			}
			if err := w.inside(c.Path); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
				return err
			}
			if err := os.Rename(c.Trashed, c.Path); err != nil {
				return fmt.Errorf("couldn't bring %s back from the Trash: %w", filepath.Base(c.Path), err)
			}
			if w.TrashInfo {
				_ = os.Remove(w.trashInfo(c.Trashed)) // no longer in the Trash
			}
		case c.Before != "":
			b, err := os.ReadFile(c.Before)
			if err != nil {
				return err
			}
			if err := w.File(c.Path, fromHistory(c.Path, b)); err != nil {
				return err
			}
		default: // the change created this file
			if exists(c.Path) {
				if _, err := w.Remove(c.Path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *Writer) snapDir(path string) string {
	// Readable name for browsing, plus a hash of the full path so two files never share a folder.
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return filepath.Join(w.History, filepath.Base(path)+"-"+hex.EncodeToString(sum[:6]))
}

// snapshot saves path's current content, tagged with who made it, and returns the version path.
// Identical consecutive versions are not duplicated; missing files and folders return "".
func (w *Writer) snapshot(path, who string) (string, error) {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return "", nil // folders go to the Trash whole; versions are per file
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	b = forHistory(path, b)
	if v := w.Versions(path); len(v) > 0 {
		if last, err := os.ReadFile(v[0].Path); err == nil && bytes.Equal(last, b) {
			return v[0].Path, nil
		}
	}
	dir := w.snapDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	snap := filepath.Join(dir, w.Now().UTC().Format(stampFmt)+"~"+who+".snap")
	if err := os.WriteFile(snap, b, 0o600); err != nil {
		return "", err
	}
	if v := w.Versions(path); len(v) > keepSnapshots {
		for _, old := range v[keepSnapshots:] {
			_ = os.Remove(old.Path)
		}
	}
	return snap, nil
}

// Atomic writes via a temp file and rename, so a crash never leaves a half-written file. A new
// file gets perm, and folders it creates get perm plus the matching search bits (0600 → 0700);
// an existing file keeps its mode.
func Atomic(path string, b []byte, perm os.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // write through a symlink (e.g. a dotfiles-managed CLAUDE.md), never replace it
	}
	mode := perm
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), perm|perm&0o444>>2); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cca-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil { // on disk before the rename, so a crash can't leave it empty
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Settings files can hold secrets in their env block, and history keeps no secrets: versions of
// them are saved without it, and a restore takes env from the file as it is now.
func isSettings(path string) bool {
	b := filepath.Base(path)
	return b == "settings.json" || b == "settings.local.json"
}

func forHistory(path string, b []byte) []byte {
	var doc map[string]any
	if !isSettings(path) || decode(b, &doc) != nil {
		return b
	}
	if _, ok := doc["env"]; !ok {
		return b
	}
	delete(doc, "env")
	out, err := encode(doc)
	if err != nil {
		return b
	}
	return out
}

func fromHistory(path string, snap []byte) []byte {
	var doc, cur map[string]any
	if !isSettings(path) || decode(snap, &doc) != nil {
		return snap
	}
	if b, err := os.ReadFile(path); err == nil && decode(b, &cur) == nil && cur["env"] != nil {
		doc["env"] = cur["env"]
	} else if _, had := doc["env"]; !had {
		return snap
	}
	out, err := encode(doc)
	if err != nil {
		return snap
	}
	return out
}

// read is os.ReadFile for a file cca is about to change. Files over scan.MaxText are refused: the
// scan and the editor only ever see that much, so changing one would cut off its tail.
func readCapped(path string) ([]byte, error) {
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() && fi.Size() > scan.MaxText {
		return nil, fmt.Errorf("%s is too large to change here (over 2 MB): edit it in a text editor", filepath.Base(path))
	}
	return os.ReadFile(path)
}

// linkOK decides whether a write may go through path when path itself is a symlink. Links in the
// user's own Claude folder are theirs (dotfiles, say): fine, as long as the target is the same kind
// of file. A link anywhere else came with a repo, so its target must stay inside that repo: a
// committed notes.md -> ~/.zshrc or -> ~/.claude/CLAUDE.md must never be written through.
func (w *Writer) linkOK(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	target, err := filepath.EvalSymlinks(path)
	refuse := fmt.Errorf("%s is a link to another file, so it isn't changed from here", filepath.Base(path))
	if err != nil || !strings.EqualFold(filepath.Ext(target), filepath.Ext(path)) || filepath.Ext(path) == "" {
		return refuse
	}
	if under(path, scan.ConfigDir(w.Home)) {
		return nil
	}
	repo := filepath.Dir(path)
	for !exists(filepath.Join(repo, ".git")) {
		if up := filepath.Dir(repo); up != repo {
			repo = up
			continue
		}
		return refuse // not in a repo either
	}
	if real, err := filepath.EvalSymlinks(repo); err != nil || !under(target, real) {
		return refuse
	}
	return nil
}

func under(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && filepath.IsLocal(rel)
}
