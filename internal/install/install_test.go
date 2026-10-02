package install

import (
	"strings"
	"testing"
)

func one(t *testing.T, text string) Server {
	t.Helper()
	p, err := Parse(text)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if p.Kind != "mcp" || len(p.Servers) != 1 {
		t.Fatalf("want one server, got %+v", p)
	}
	return p.Servers[0]
}

func TestSnippetShapesFromDocs(t *testing.T) {
	// Claude Code / Claude Desktop / Cursor
	s := one(t, `{"mcpServers":{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]}}}`)
	if s.Name != "playwright" || s.Type != "stdio" || s.Command != "npx" || s.Args[0] != "@playwright/mcp@latest" {
		t.Errorf("mcpServers: %+v", s)
	}
	if got := s.JSON(); got != `{"args":["@playwright/mcp@latest"],"command":"npx","type":"stdio"}` {
		t.Errorf("JSON: %s", got)
	}
	// VS Code, with an input prompt and JSONC comments and trailing commas
	s = one(t, `{
	  // from the docs
	  "inputs": [{"id": "gh-token", "type": "promptString"}],
	  "servers": {"github": {"type": "http", "url": "https://api.example.com/mcp/", /* note */
	    "headers": {"Authorization": "Bearer ${input:gh-token}"},},},
	}`)
	if s.Type != "http" || s.Headers["Authorization"] != "Bearer ${GH_TOKEN}" || s.URL != "https://api.example.com/mcp/" {
		t.Errorf("vscode: %+v", s)
	}
	// VS Code settings.json, ${env:X}
	s = one(t, `{"mcp":{"servers":{"x":{"command":"node","args":["s.js"],"env":{"API_KEY":"${env:MY_KEY}","PORT":3000}}}}}`)
	if s.Env["API_KEY"] != "${MY_KEY}" || s.Env["PORT"] != "3000" {
		t.Errorf("settings.json: %+v", s)
	}
	// a bare server object, and streamable-http
	s = one(t, `{"type":"streamable-http","url":"https://mcp.example.com/mcp"}`)
	if s.Name != "" || s.Type != "http" {
		t.Errorf("bare: %+v", s)
	}
	// fields Claude Code doesn't read are dropped, with a note
	p, _ := Parse(`{"mcpServers":{"a":{"command":"x","disabled":false,"alwaysAllow":["t"]}}}`)
	if len(p.Notes) != 2 || strings.Contains(p.Servers[0].JSON(), "alwaysAllow") {
		t.Errorf("unknown fields: %+v", p)
	}
	// a URL in a string is not a comment
	if s := one(t, `{"x":{"url":"https://a.example//b"}}`); s.URL != "https://a.example//b" {
		t.Errorf("jsonc stripped a url: %q", s.URL)
	}
}

func TestCommandsFromDocs(t *testing.T) {
	s := one(t, `claude mcp add my-server -e API_KEY=xxx -e REGION=eu -- npx my-mcp-server --flag`)
	if s.Name != "my-server" || s.Command != "npx" || strings.Join(s.Args, " ") != "my-mcp-server --flag" || s.Env["REGION"] != "eu" {
		t.Errorf("mcp add stdio: %+v", s)
	}
	s = one(t, `$ claude mcp add --transport http corridor https://app.corridor.dev/api/mcp --header "Authorization: Bearer abc"`)
	if s.Type != "http" || s.URL != "https://app.corridor.dev/api/mcp" || s.Headers["Authorization"] != "Bearer abc" {
		t.Errorf("mcp add http: %+v", s)
	}
	s = one(t, `claude mcp add-json weather '{"type":"http","url":"https://api.weather.com/mcp"}'`)
	if s.Name != "weather" || s.URL != "https://api.weather.com/mcp" {
		t.Errorf("add-json: %+v", s)
	}
	p, err := Parse("/plugin marketplace add anthropics/claude-code\n/plugin install feature-dev@claude-code")
	if err != nil || p.Kind != "plugin" || p.Marketplace != "anthropics/claude-code" || p.Plugin != "feature-dev@claude-code" {
		t.Errorf("plugin: %+v %v", p, err)
	}
	p, err = Parse("claude plugin install context-admin@context-admin --scope project")
	if err != nil || p.Plugin != "context-admin@context-admin" {
		t.Errorf("plugin install with scope: %+v %v", p, err)
	}
}

func TestRejectsWhatItCantTrust(t *testing.T) {
	for _, bad := range []string{
		"rm -rf ~",
		"claude mcp add x",                // no command
		`claude mcp add 'x`,               // unclosed quote
		`{"mcpServers":{"a":{"nope":1}}}`, // no server
		`{"mcpServers":{"a":{"type":"ws","url":"wss://x"}}}`,
		"claude config set foo bar",
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// a pasted command is split into words, never handed to a shell
	s := one(t, `claude mcp add x -- sh -c "echo hi; touch /tmp/pwned"`)
	if s.Command != "sh" || s.Args[1] != "echo hi; touch /tmp/pwned" {
		t.Errorf("words: %+v", s)
	}
}

func TestCheckServer(t *testing.T) {
	ok := Server{Name: "pw", Type: "stdio", Command: "npx", Env: map[string]string{"API_KEY": "${API_KEY}"}}
	if err := CheckServer(ok, "project"); err != nil {
		t.Errorf("reference in project scope: %v", err)
	}
	literal := ok
	literal.Env = map[string]string{"API_KEY": "sk-live-123"}
	if err := CheckServer(literal, "project"); err == nil {
		t.Error("a literal secret must not go into the committed .mcp.json")
	}
	if err := CheckServer(literal, "user"); err == nil || len(literal.Literals()) != 1 {
		t.Errorf("a key written out would sit on the add-json command line in any scope: %v %v", err, literal.Literals())
	}
	hdr := Server{Name: "r", Type: "http", URL: "https://x.example", Headers: map[string]string{"Authorization": "Bearer abc"}}
	if err := CheckServer(hdr, "project"); err == nil {
		t.Error("a literal bearer header must not be committed")
	}
	for _, bad := range []Server{
		{Name: "a b", Type: "stdio", Command: "x"},
		{Name: "a", Type: "stdio"},
		{Name: "a", Type: "http", URL: "file:///etc/passwd"},
		{Name: "a", Type: "http", URL: "https://x.example", Command: "x"},
		{Name: "a", Type: "ws", URL: "wss://x"},
	} {
		if CheckServer(bad, "user") == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if CheckServer(ok, "global") == nil {
		t.Error("unknown scope accepted")
	}
}

func TestCheckPluginAndSkill(t *testing.T) {
	dir := func(p string) bool { return p == "/Users/me/plugins" }
	for _, ok := range [][2]string{{"anthropics/claude-code", "x@claude-code"}, {"https://git.example.com/a.git", ""}, {"/Users/me/plugins", "p@m"}, {"", "p@m"}} {
		if err := CheckPlugin(ok[0], ok[1], dir); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for _, bad := range [][2]string{{"http://insecure.example/a", ""}, {"../../etc", ""}, {"/Users/me/other", ""}, {"", "x; rm"}, {"", ""}, {"git@github.com:a/b.git", ""}} {
		if CheckPlugin(bad[0], bad[1], dir) == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	p, err := Parse("---\nname: release-notes\ndescription: \"Draft notes: from PRs\"\n---\nDo it.\n")
	if err != nil || p.Skill.Name != "release-notes" || p.Skill.Description != "Draft notes: from PRs" || p.Skill.Body != "Do it." {
		t.Fatalf("skill: %+v %v", p.Skill, err)
	}
	if err := CheckSkill(*p.Skill); err != nil {
		t.Error(err)
	}
	if got := SkillFile(*p.Skill); !strings.Contains(got, `description: "Draft notes: from PRs"`) {
		t.Errorf("a colon in the description must stay valid YAML:\n%s", got)
	}
	for _, bad := range []Skill{{Name: "Bad Name", Description: "d"}, {Name: "synced", Description: "d"}, {Name: "ok"}, {Name: "../x", Description: "d"}} {
		if CheckSkill(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestKeysHiddenOutsideEnvAndHeaders(t *testing.T) {
	for _, s := range []Server{
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"x", "--api-key", "abc123def456"}},
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"--token=abc123def456"}},
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"sk-live-abcdefghijkl"}},
		{Name: "a", Type: "http", URL: "https://u:p@x.example/mcp"},
		{Name: "a", Type: "http", URL: "https://x.example/mcp?api_key=abc"},
		{Name: "a", Type: "http", URL: "https://x.example", Headers: map[string]string{"Authorization": "Bearer sk-1 ${X}"}},
		{Name: "a", Type: "stdio", Command: "x", Env: map[string]string{"GITHUB_PAT": "abc"}},
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"-k", "abc123def456"}},
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"--api-key", "-abc123def"}},
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"--api-key=${KEY}abc123"}},
		{Name: "a", Type: "stdio", Command: "sh", Args: []string{"-c", "API_KEY=abc123def npx x"}},
		{Name: "a", Type: "http", URL: "https://mcp.example.com/api/mcp/s/a8f3k2l9q0w8e7r6t5y4/mcp"},
		{Name: "a", Type: "http", URL: "https://x.example/mcp?k=abcdef1234567890"},
	} {
		if CheckServer(s, "user") == nil {
			t.Errorf("accepted a key written out: %+v", s)
		}
	}
	for _, s := range []Server{
		{Name: "a", Type: "stdio", Command: "npx", Args: []string{"--auth-type", "oauth", "--api-key", "${KEY}"}},
		{Name: "a", Type: "http", URL: "https://x.example/mcp?region=eu", Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
	} {
		if err := CheckServer(s, "user"); err != nil {
			t.Errorf("refused a clean definition: %+v: %v", s, err)
		}
	}
}
