package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/audit"
	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

func start(t *testing.T) (*Server, string, context.CancelFunc) { return startWith(t, false) }

// startWith starts a server; readOnly is fixed at start because background goroutines read it.
func startWith(t *testing.T, readOnly bool) (*Server, string, context.CancelFunc) {
	t.Helper()
	home := t.TempDir()
	mem := filepath.Join(home, ".claude", "projects", "-x", "memory")
	_ = os.MkdirAll(mem, 0o755)
	_ = os.WriteFile(filepath.Join(mem, "a.md"), []byte("---\nname: a\n---\nhi\n"), 0o644)
	s := &Server{Home: home, ReadOnly: readOnly, Static: fstest.MapFS{"index.html": {Data: []byte("<title>cca</title>")}}}
	ln, url, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx, ln) }()
	for !ready(s) { // Serve does the first scan before accepting requests
		time.Sleep(10 * time.Millisecond)
	}
	return s, url, cancel
}

func TestTokenCookieAndState(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	base := "http://" + s.addr

	// No token: refused.
	res, err := http.Get(base + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", res.StatusCode)
	}

	// The launch URL sets a cookie and redirects to a clean URL.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err = c.Get(url)
	if err != nil || res.StatusCode != http.StatusOK || res.Request.URL.RawQuery != "" {
		t.Fatalf("launch: %v %v %v", err, res.StatusCode, res.Request.URL)
	}
	res, err = c.Get(base + "/api/state")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("state: %v %v", err, res.StatusCode)
	}
	var st State
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil || st.Report.Counts["memory"] != 1 {
		t.Fatalf("state body: %v %+v", err, st.Report)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header must never be sent")
	}
}

func TestRejectsForeignHost(t *testing.T) {
	s, _, cancel := start(t)
	defer cancel()
	req, _ := http.NewRequest("GET", "http://"+s.addr+"/?t="+s.token, nil)
	req.Host = "evil.example:" + portOf(s.addr) // DNS rebinding presents a foreign Host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host: %d", res.StatusCode)
	}
}

func TestLiveReloadOnMemoryChange(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if _, err := c.Get(url); err != nil {
		t.Fatal(err)
	}
	res, err := c.Get("http://" + s.addr + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var all strings.Builder
		for {
			n, err := res.Body.Read(buf)
			all.Write(buf[:n])
			if strings.Contains(all.String(), "event: changed") || err != nil {
				got <- all.String()
				return
			}
		}
	}()
	time.Sleep(150 * time.Millisecond) // let the watcher register
	mem := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "b.md")
	_ = os.WriteFile(mem, []byte("---\nname: b\n---\n"), 0o644)
	select {
	case out := <-got:
		if !strings.Contains(out, "event: changed") {
			t.Fatalf("stream: %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live-reload event after a memory file was added")
	}
}

func ready(s *Server) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state != nil
}

func TestPrefsRoundTripInReadOnly(t *testing.T) {
	s, url, cancel := startWith(t, true) // prefs are the app's own file, so they still save in read-only mode
	defer cancel()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if _, err := c.Get(url); err != nil {
		t.Fatal(err)
	}
	body := `{"projects":{"/r":{"alias":"Work","favorite":true}},"order":["/r","Global"]}`
	req, _ := http.NewRequest("PUT", "http://"+s.addr+"/api/prefs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("put: %v %v", err, res.StatusCode)
	}
	res, _ = c.Get("http://" + s.addr + "/api/prefs")
	var p Prefs
	_ = json.NewDecoder(res.Body).Decode(&p)
	if p.Projects["/r"].Alias != "Work" || !p.Projects["/r"].Favorite || len(p.Order) != 2 {
		t.Fatalf("prefs: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".claude-context-admin", "prefs.json")); err != nil {
		t.Fatal("prefs for a --home run must stay inside that home")
	}
}

func signedIn(t *testing.T, url string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if _, err := c.Get(url); err != nil {
		t.Fatal(err)
	}
	return c
}

func post(t *testing.T, c *http.Client, s *Server, op string, a map[string]any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"op": op, "args": a})
	res, err := c.Post("http://"+s.addr+"/api/act", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Message string }
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out.Message
}

func TestActValidatesAndWrites(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	c := signedIn(t, url)
	a := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "a.md")

	// Paths outside the inventory are refused, even if they exist.
	if code, _ := post(t, c, s, "file-save", map[string]any{"path": "/etc/hosts", "content": "x"}); code != http.StatusBadRequest {
		t.Fatalf("arbitrary path accepted: %d", code)
	}
	code, msg := post(t, c, s, "memory-save", map[string]any{"path": a, "name": "a", "type": "reference", "description": "d", "body": "new"})
	if code != http.StatusOK {
		t.Fatalf("save: %d %s", code, msg)
	}
	if b, _ := os.ReadFile(a); !strings.Contains(string(b), "new") || !strings.Contains(string(b), "type: reference") {
		t.Fatalf("file: %s", b)
	}
	if code, _ := post(t, c, s, "no-such-op", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown op: %d", code)
	}
	ro, roURL, roCancel := startWith(t, true)
	defer roCancel()
	roA := filepath.Join(ro.Home, ".claude", "projects", "-x", "memory", "a.md")
	if code, _ := post(t, signedIn(t, roURL), ro, "memory-trash", map[string]any{"path": roA}); code != http.StatusForbidden {
		t.Fatalf("read-only: %d", code)
	}
}

func TestActPluginUsesCLI(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	var got []string
	s.CLI = func(_ context.Context, _ string, a ...string) ([]byte, error) { got = a; return []byte("{}"), nil }
	s.mu.Lock()
	s.inv.Entries = append(s.inv.Entries, entryPlugin("p1@mk"))
	s.mu.Unlock()
	c := signedIn(t, url)
	if code, msg := post(t, c, s, "plugin-disable", map[string]any{"id": "p1@mk"}); code != http.StatusOK {
		t.Fatalf("%d %s", code, msg)
	}
	if strings.Join(got, " ") != "plugin disable p1@mk --json --scope user" {
		t.Fatalf("cli args: %v", got)
	}
}

func entryPlugin(id string) entry.Entry {
	return entry.Entry{Kind: entry.Plugin, Name: strings.Split(id, "@")[0], Enabled: true,
		Meta: map[string]any{"id": id, "installScope": "user"}}
}

func TestActivityUndoAndClaudeEdits(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	c := signedIn(t, url)
	a := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "a.md")
	orig, _ := os.ReadFile(a)
	if code, msg := post(t, c, s, "memory-save", map[string]any{"path": a, "description": "d", "body": "edited"}); code != http.StatusOK {
		t.Fatalf("save: %d %s", code, msg)
	}
	acts := activity(t, c, s)
	if len(acts) == 0 || acts[0].Who != "you" || !acts[0].CanUndo {
		t.Fatalf("activity: %+v", acts)
	}
	undo(t, c, s, acts[0].ID)
	if b, _ := os.ReadFile(a); string(b) != string(orig) {
		t.Fatalf("undo didn't restore:\n%s", b)
	}

	// Claude edits the memory on disk; the watcher records it and it can be undone.
	time.Sleep(200 * time.Millisecond)
	_ = os.WriteFile(a, []byte("---\nname: a\n---\nClaude wrote this\n"), 0o644)
	var claude *activityView
	for i := 0; i < 50 && claude == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, x := range activity(t, c, s) {
			if x.Who == "claude" {
				x := x
				claude = &x
				break
			}
		}
	}
	if claude == nil || !claude.CanUndo {
		t.Fatal("Claude's edit was not recorded as undoable activity")
	}
	undo(t, c, s, claude.ID)
	if b, _ := os.ReadFile(a); string(b) != string(orig) {
		t.Fatalf("undoing Claude's edit didn't restore:\n%s", b)
	}
}

func activity(t *testing.T, c *http.Client, s *Server) []activityView {
	t.Helper()
	res, err := c.Get("http://" + s.addr + "/api/activity")
	if err != nil {
		t.Fatal(err)
	}
	var out []activityView
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out
}

func undo(t *testing.T, c *http.Client, s *Server, id string) {
	t.Helper()
	res, err := c.Post("http://"+s.addr+"/api/undo", "application/json", strings.NewReader(`{"id":"`+id+`"}`))
	if err != nil || res.StatusCode != http.StatusOK {
		var m struct{ Message string }
		_ = json.NewDecoder(res.Body).Decode(&m)
		t.Fatalf("undo: %v %d %s", err, res.StatusCode, m.Message)
	}
}

func TestRejectsCrossSiteWrites(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	c := signedIn(t, url)
	a := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "a.md")
	body := `{"op":"memory-trash","args":{"path":"` + a + `"}}`
	for name, h := range map[string]map[string]string{
		// a page on another localhost port: same-site, so the cookie is sent
		"other port": {"Content-Type": "application/json", "Origin": "http://127.0.0.1:3000", "Sec-Fetch-Site": "same-site"},
		// an HTML form can only send these content types
		"form post": {"Content-Type": "text/plain"},
	} {
		req, _ := http.NewRequest("POST", "http://"+s.addr+"/api/act", strings.NewReader(body))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode < 400 {
			t.Errorf("%s: accepted with %d", name, res.StatusCode)
		}
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("memory was trashed by a cross-site request")
	}
}

func TestFailedRenameIsPutBack(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	a := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "a.md")
	other := filepath.Join(s.Home, ".claude", "projects", "-y", "memory")
	_ = os.MkdirAll(other, 0o755)
	_ = os.WriteFile(filepath.Join(other, "b.md"), []byte("---\nname: b\n---\nsee [[a]]\n"), 0o644)
	s.Refresh(context.Background(), false)
	_ = os.WriteFile(filepath.Join(other, ".consolidate-lock"), nil, 0o644) // Claude consolidating there
	code, _ := post(t, signedIn(t, url), s, "memory-save", map[string]any{"path": a, "name": "Renamed", "description": "d", "body": "changed"})
	if code != http.StatusConflict {
		t.Fatalf("want 409, got %d", code)
	}
	if b, err := os.ReadFile(a); err != nil || strings.Contains(string(b), "changed") {
		t.Fatalf("original not put back: %v %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(a), "renamed.md")); err == nil {
		t.Fatal("renamed copy left behind")
	}
}

func TestMergeGlobalWithSameNamedCopies(t *testing.T) {
	for _, keepGlobal := range []bool{true, false} {
		s, url, cancel := start(t)
		proj := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "a.md")
		glob := filepath.Join(s.globalMemoryDir(), "a.md")
		_ = os.MkdirAll(filepath.Dir(glob), 0o755)
		_ = os.WriteFile(glob, []byte("---\nname: a\n---\nglobal copy\n"), 0o644)
		s.Refresh(context.Background(), false)
		keep, drop := proj, glob
		if keepGlobal {
			keep, drop = glob, proj
		}
		code, msg := post(t, signedIn(t, url), s, "merge-global", map[string]any{"keep": keep, "drop": drop})
		if code != http.StatusOK {
			t.Fatalf("keepGlobal=%v: %d %s", keepGlobal, code, msg)
		}
		if _, err := os.Stat(proj); err == nil {
			t.Errorf("keepGlobal=%v: project copy still there", keepGlobal)
		}
		want := map[bool]string{true: "global copy", false: "hi"}[keepGlobal]
		if b, _ := os.ReadFile(glob); !strings.Contains(string(b), want) {
			t.Errorf("keepGlobal=%v: kept the wrong text: %s", keepGlobal, b)
		}
		cancel()
	}
}

func TestRelocateMemoriesOfAMovedFolder(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	oldPath, newPath := filepath.Join(s.Home, "dev", "old-site"), filepath.Join(s.Home, "dev", "new-site")
	_ = os.MkdirAll(newPath, 0o755)
	from := filepath.Join(s.Home, ".claude", "projects", scan.Encode(oldPath), "memory")
	_ = os.MkdirAll(from, 0o755)
	_ = os.WriteFile(filepath.Join(from, "project_site.md"), []byte("---\nname: site\n---\nThe new-site repo\n"), 0o644)
	_ = os.WriteFile(filepath.Join(from, "MEMORY.md"), []byte("- [Site](project_site.md) — the site\n"), 0o644)
	idx := `{"version":1,"entries":[],"originalPath":"` + oldPath + `"}`
	_ = os.WriteFile(filepath.Join(filepath.Dir(from), "sessions-index.json"), []byte(idx), 0o644)
	s.Refresh(context.Background(), false)

	var f *audit.Finding
	s.mu.RLock()
	findings := s.state.Report.Findings
	s.mu.RUnlock()
	for _, x := range findings {
		if x.Code == "folder-moved" {
			f = &x
		}
	}
	if f == nil || f.Detail != newPath {
		t.Fatalf("finding: %+v", f)
	}
	c := signedIn(t, url)
	if code, _ := post(t, c, s, "project-relocate", map[string]any{"from": s.globalMemoryDir(), "to": newPath}); code != http.StatusBadRequest {
		t.Fatalf("a live project's memories must not be movable this way: %d", code)
	}
	if code, msg := post(t, c, s, "project-relocate", map[string]any{"from": from, "to": newPath}); code != http.StatusOK {
		t.Fatalf("relocate: %d %s", code, msg)
	}
	dest := filepath.Join(s.Home, ".claude", "projects", scan.Encode(newPath), "memory")
	if b, err := os.ReadFile(filepath.Join(dest, "MEMORY.md")); err != nil || !strings.Contains(string(b), "project_site.md") {
		t.Fatalf("new index: %v %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(from, "project_site.md")); err == nil {
		t.Fatal("old copy left behind")
	}
}

func TestFirstMemoryInAProject(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	repo := filepath.Join(s.Home, "dev", "fresh")
	_ = os.MkdirAll(repo, 0o755)
	pd := filepath.Join(s.Home, ".claude", "projects", scan.Encode(repo))
	_ = os.MkdirAll(pd, 0o755)
	_ = os.WriteFile(filepath.Join(pd, "s.jsonl"), []byte(`{"cwd":"`+repo+`"}`+"\n"), 0o644)
	s.Refresh(context.Background(), false)
	dir := filepath.Join(pd, "memory")
	if code, msg := post(t, signedIn(t, url), s, "memory-create", map[string]any{"dir": dir, "title": "First one", "type": "project", "description": "d", "body": "b"}); code != http.StatusOK {
		t.Fatalf("first memory: %d %s", code, msg)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "MEMORY.md")); err != nil || !strings.Contains(string(b), "First one") {
		t.Fatalf("index: %v %s", err, b)
	}
	if code, _ := post(t, signedIn(t, url), s, "memory-create", map[string]any{"dir": filepath.Join(s.Home, "elsewhere"), "title": "x", "type": "project"}); code != http.StatusBadRequest {
		t.Fatalf("an arbitrary folder must be refused: %d", code)
	}
}

func TestEmptyHomeSendsEmptyLists(t *testing.T) {
	s := &Server{Home: t.TempDir()}
	s.Refresh(context.Background(), false)
	b, _ := json.Marshal(s.state)
	for _, k := range []string{`"projects":null`, `"entries":null`, `"findings":null`, `"proposals":null`} {
		if strings.Contains(string(b), k) {
			t.Errorf("%s: a new user's page breaks on null", k)
		}
	}
}

func TestAddMCPServerThroughTheCLI(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	var calls [][]string
	var dirs []string
	s.CLI = func(_ context.Context, dir string, a ...string) ([]byte, error) {
		calls, dirs = append(calls, a), append(dirs, dir)
		return []byte("ok"), nil
	}
	c := signedIn(t, url)
	srv := map[string]any{"name": "pw", "type": "stdio", "command": "npx", "args": []string{"@playwright/mcp@latest"},
		"env": map[string]string{"API_KEY": "${PW_KEY}"}, "evil": "ignored"}
	if code, msg := post(t, c, s, "mcp-add", map[string]any{"server": srv, "scope": "user"}); code != http.StatusOK {
		t.Fatalf("%d %s", code, msg)
	}
	want := []string{"mcp", "add-json", "pw", `{"args":["@playwright/mcp@latest"],"command":"npx","env":{"API_KEY":"${PW_KEY}"},"type":"stdio"}`, "--scope", "user"}
	if strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") || dirs[0] != s.Home {
		t.Fatalf("cli: %q in %s", calls[0], dirs[0])
	}
	// a literal secret can't go into a repo's .mcp.json, and an unknown folder is refused
	srv["env"] = map[string]string{"API_KEY": "sk-live-1"}
	if code, _ := post(t, c, s, "mcp-add", map[string]any{"server": srv, "scope": "project", "project": s.Home}); code != http.StatusBadRequest {
		t.Fatalf("literal secret in project scope: %d", code)
	}
	srv["env"] = nil
	if code, _ := post(t, c, s, "mcp-add", map[string]any{"server": srv, "scope": "local", "project": "/etc"}); code != http.StatusBadRequest {
		t.Fatalf("arbitrary folder: %d", code)
	}
	if len(calls) != 1 {
		t.Fatalf("refused adds must not reach the CLI: %q", calls)
	}
}

func TestPluginInstallNeedsConfirmingItsCommand(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	var calls []string
	s.CLI = func(_ context.Context, _ string, a ...string) ([]byte, error) {
		cmd := strings.Join(a, " ")
		calls = append(calls, cmd)
		switch {
		case strings.Contains(cmd, "marketplace add"):
			return []byte(`{"command":"marketplace-add","outcome":"ok","marketplace":"mk"}`), nil
		case strings.Contains(cmd, "--accept-command 2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"):
			return []byte(`{"command":"install","outcome":"ok","pluginId":"p@mk"}`), nil
		default:
			return []byte(`{"command":"install","outcome":"failed","message":"This plugin runs a command to install.","shownCommand":{"sha256":"2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881","command":"npm i x"}}`), errors.New("exit 1")
		}
	}
	c := signedIn(t, url)
	args := map[string]any{"marketplace": "owner/repo", "plugin": "p@mk", "scope": "user"}
	b, _ := json.Marshal(map[string]any{"op": "plugin-add", "args": args})
	res, err := c.Post("http://"+s.addr+"/api/act", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Confirm struct {
			SHA     string         `json:"sha256"`
			Command map[string]any `json:"command"`
		}
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != http.StatusConflict || out.Confirm.SHA != "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881" || out.Confirm.Command["command"] != "npm i x" {
		t.Fatalf("confirm: %d %+v", res.StatusCode, out)
	}
	args["accept"] = "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"
	if code, msg := post(t, c, s, "plugin-add", args); code != http.StatusOK {
		t.Fatalf("accepted: %d %s", code, msg)
	}
	for _, cl := range calls {
		if strings.Contains(cl, " -y") || strings.Contains(cl, "--yes") {
			t.Fatalf("never auto-confirm: %s", cl)
		}
	}
	if code, _ := post(t, c, s, "plugin-add", map[string]any{"marketplace": "http://plain.example/x", "plugin": "p@mk", "scope": "user"}); code != http.StatusBadRequest {
		t.Fatalf("plain http marketplace: %d", code)
	}
}

func TestCreateSkill(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	c := signedIn(t, url)
	if code, msg := post(t, c, s, "skill-create", map[string]any{"name": "release-notes", "description": "Draft notes: from PRs", "body": "Do it.", "scope": "user"}); code != http.StatusOK {
		t.Fatalf("%d %s", code, msg)
	}
	b, err := os.ReadFile(filepath.Join(s.Home, ".claude", "skills", "release-notes", "SKILL.md"))
	if err != nil || !strings.Contains(string(b), `description: "Draft notes: from PRs"`) {
		t.Fatalf("%v %s", err, b)
	}
	for _, bad := range []map[string]any{
		{"name": "release-notes", "description": "again", "scope": "user"},
		{"name": "../../evil", "description": "d", "scope": "user"},
		{"name": "x", "description": "d", "scope": "project", "project": "/tmp"},
	} {
		if code, _ := post(t, c, s, "skill-create", bad); code != http.StatusBadRequest {
			t.Errorf("accepted %v: %d", bad, code)
		}
	}
}

func TestInstallParseChangesNothing(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	called := false
	s.CLI = func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil }
	b, _ := json.Marshal(map[string]string{"text": `{"mcpServers":{"gh":{"command":"npx","env":{"GITHUB_TOKEN":"ghp_x"}}}}`})
	res, err := signedIn(t, url).Post("http://"+s.addr+"/api/install/parse", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Plan     struct{ Servers []struct{ Name string } }
		Literals map[string][]string
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 200 || out.Plan.Servers[0].Name != "gh" || out.Literals["gh"][0] != "GITHUB_TOKEN" || called {
		t.Fatalf("%d %+v called=%v", res.StatusCode, out, called)
	}
}

func TestCLIErrorsNeverCarryArguments(t *testing.T) {
	bin := t.TempDir()
	_ = os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho 'MCP server pw already exists in user config'\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := ClaudeIn(context.Background(), t.TempDir(), t.TempDir(), "mcp", "add-json", "pw",
		`{"args":["--header","Authorization: Bearer sk-live-SECRET"],"command":"npx","env":{"API_KEY":"sk-live-SECRET"}}`, "--scope", "user")
	if err == nil || strings.Contains(err.Error(), "SECRET") || cliMessage(err) != "MCP server pw already exists in user config." {
		t.Fatalf("error: %v / %q", err, cliMessage(err))
	}
}

func TestAcceptRunsOnlySuggestableOps(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	called := false
	s.CLI = func(context.Context, string, ...string) ([]byte, error) {
		called = true
		return []byte(`{"outcome":"ok"}`), nil
	}
	dir := write.DataDir(s.Home)
	_ = os.MkdirAll(dir, 0o700)
	line := `{"id":"evil","op":"mcp-add","args":{"scope":"user","server":{"name":"x","type":"stdio","command":"sh"}},"reason":"tidy","status":"pending"}` + "\n"
	_ = os.WriteFile(filepath.Join(dir, "proposals.jsonl"), []byte(line), 0o600)
	b, _ := json.Marshal(map[string]any{"id": "evil", "accept": true})
	res, err := signedIn(t, url).Post("http://"+s.addr+"/api/proposals/decide", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest || called {
		t.Fatalf("a hand-written proposal ran: %d called=%v", res.StatusCode, called)
	}
}

func TestFlagShapedInputsAndScopedUndo(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	var calls []string
	var dirs []string
	s.CLI = func(_ context.Context, dir string, a ...string) ([]byte, error) {
		calls, dirs = append(calls, strings.Join(a, " ")), append(dirs, dir)
		return []byte(`{"outcome":"ok","pluginId":"p@mk"}`), nil
	}
	c := signedIn(t, url)
	if code, _ := post(t, c, s, "mcp-add", map[string]any{"server": map[string]any{"name": "-h", "type": "stdio", "command": "x"}, "scope": "user"}); code != http.StatusBadRequest {
		t.Fatalf("flag-shaped server name: %d", code)
	}
	for _, bad := range []map[string]any{{"plugin": "--registry", "scope": "user"}, {"plugin": "p@mk", "scope": "user", "accept": "-y"}} {
		if code, _ := post(t, c, s, "plugin-add", bad); code != http.StatusBadRequest {
			t.Fatalf("accepted %v: %d", bad, code)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("refused requests reached the CLI: %q", calls)
	}
	// a project-scope install is undone in that project, at that scope
	repo := filepath.Join(s.Home, "dev", "app")
	_ = os.MkdirAll(repo, 0o755)
	pd := filepath.Join(s.Home, ".claude", "projects", scan.Encode(repo))
	_ = os.MkdirAll(pd, 0o755)
	_ = os.WriteFile(filepath.Join(pd, "s.jsonl"), []byte(`{"cwd":"`+repo+`"}`+"\n"), 0o644)
	s.Refresh(context.Background(), false)
	b, _ := json.Marshal(map[string]any{"op": "plugin-add", "args": map[string]any{"plugin": "p@mk", "scope": "project", "project": repo}})
	res, _ := c.Post("http://"+s.addr+"/api/act", "application/json", strings.NewReader(string(b)))
	var out struct{ Activity string }
	_ = json.NewDecoder(res.Body).Decode(&out)
	b, _ = json.Marshal(map[string]string{"id": out.Activity})
	if res, _ := c.Post("http://"+s.addr+"/api/undo", "application/json", strings.NewReader(string(b))); res.StatusCode != http.StatusOK {
		t.Fatalf("undo: %d", res.StatusCode)
	}
	if last := calls[len(calls)-1]; last != "plugin uninstall p@mk --json --scope project --yes" || dirs[len(dirs)-1] != repo {
		t.Fatalf("undo ran %q in %s", last, dirs[len(dirs)-1])
	}
	// a skills folder that links elsewhere is refused
	elsewhere := t.TempDir()
	_ = os.Symlink(elsewhere, filepath.Join(repo, ".claude"))
	if code, _ := post(t, c, s, "skill-create", map[string]any{"name": "x", "description": "d", "scope": "project", "project": repo}); code != http.StatusBadRequest {
		t.Fatalf("symlinked .claude: %d", code)
	}
	if des, _ := os.ReadDir(elsewhere); len(des) != 0 {
		t.Fatal("wrote through the symlink")
	}
}

func TestMergeIntoItselfIsRefused(t *testing.T) {
	s, url, cancel := start(t)
	defer cancel()
	a := filepath.Join(s.Home, ".claude", "projects", "-x", "memory", "a.md")
	if code, _ := post(t, signedIn(t, url), s, "merge-global", map[string]any{"keep": a, "drop": a}); code != http.StatusBadRequest {
		t.Fatalf("%d", code)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("memory was trashed")
	}
}
