// Package install turns what install docs publish (an MCP JSON snippet, a `claude mcp add`
// line, `/plugin` commands, a SKILL.md) into a validated plan. It only parses and checks:
// cca applies a plan through Claude Code's own CLI (MCP servers, plugins) or its versioned
// write path (skills), and makes no network requests itself.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Server is one MCP server, rebuilt from the paste with only the fields Claude Code reads.
type Server struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"` // stdio, http or sse
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Plan is what a paste would add.
type Plan struct {
	Kind        string   `json:"kind"` // mcp, plugin or skill
	Servers     []Server `json:"servers,omitempty"`
	Marketplace string   `json:"marketplace,omitempty"` // owner/repo, https URL or a local folder
	Plugin      string   `json:"plugin,omitempty"`      // name@marketplace
	Skill       *Skill   `json:"skill,omitempty"`
	Notes       []string `json:"notes,omitempty"` // what was changed or dropped, in plain words
}

// Skill is a new SKILL.md.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

var (
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`) // never starts with "-": it would read as a flag
	skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	pluginRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(@[A-Za-z0-9][A-Za-z0-9_.-]*)?$`)
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	refRe       = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}$`)
	secretKeyRe = regexp.MustCompile(`(?i)(key|token|secret|passw|auth|bearer|cookie|credential|(^|_)pat($|_))`)
	// argKeyRe: flags that take a secret (narrower than secretKeyRe: --auth-type oauth is fine).
	argKeyRe = regexp.MustCompile(`(?i)(api[-_]?key|token|secret|passw|bearer|credential|^-+(k|key|auth)$)`)
	// envAssign: KEY=value inside an argument (sh -c "API_KEY=… npx x").
	envAssign = regexp.MustCompile(`(?i)\b[A-Z0-9_]*(key|token|secret|passw)[A-Z0-9_]*=[^\s$"']{6,}`)
	// tokenLike: well-known key formats, wherever they appear.
	tokenLike = regexp.MustCompile(`(sk-|sk_live_|ghp_|gho_|ghs_|github_pat_|glpat-|xox[abpr]-|npm_|AKIA)[A-Za-z0-9_-]{8,}`)
	// keyish: a long run of letters and digits with at least one digit, the shape of a generated key.
	keyish = regexp.MustCompile(`^[A-Za-z0-9_\-.~=+]*[0-9][A-Za-z0-9_\-.~=+]*$`)
	// authScheme: what may precede a header's ${VAR}.
	authScheme   = regexp.MustCompile(`(?i)^(bearer|basic|token)\s+`)
	vscodeInput  = regexp.MustCompile(`\$\{input:([A-Za-z0-9_.-]+)\}`)
	vscodeEnvRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)
)

// Parse reads a paste. It never runs anything.
func Parse(text string) (Plan, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return Plan{}, errors.New("paste an MCP server's JSON, a `claude mcp add` command, `/plugin` commands or a SKILL.md")
	case strings.HasPrefix(text, "{"):
		return parseJSON(text)
	case strings.HasPrefix(text, "---"):
		return parseSkill(text)
	}
	return parseCommands(text)
}

// ---------- JSON snippets ----------

func parseJSON(text string) (Plan, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONC(text)), &root); err != nil {
		return Plan{}, fmt.Errorf("that isn't valid JSON: %v", err)
	}
	p := Plan{Kind: "mcp"}
	servers := root
	switch {
	case isMap(root["mcpServers"]): // Claude Code, Claude Desktop, Cursor
		servers = root["mcpServers"].(map[string]any)
	case isMap(root["servers"]): // VS Code .vscode/mcp.json
		servers = root["servers"].(map[string]any)
	case isMap(root["mcp"]) && isMap(root["mcp"].(map[string]any)["servers"]): // VS Code settings.json
		servers = root["mcp"].(map[string]any)["servers"].(map[string]any)
	case looksLikeServer(root): // a bare server object; the user names it
		s, notes, err := serverFrom("", root)
		if err != nil {
			return Plan{}, err
		}
		p.Servers, p.Notes = []Server{s}, notes
		return p, nil
	}
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		cfg, ok := servers[name].(map[string]any)
		if !ok || !looksLikeServer(cfg) {
			continue
		}
		s, notes, err := serverFrom(name, cfg)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", name, err)
		}
		p.Servers = append(p.Servers, s)
		p.Notes = append(p.Notes, notes...)
	}
	if len(p.Servers) == 0 {
		return Plan{}, errors.New("no MCP server found in that JSON (expected a command or a url)")
	}
	return p, nil
}

func looksLikeServer(m map[string]any) bool {
	_, c := m["command"].(string)
	_, u := m["url"].(string)
	return c || u
}

// serverFrom keeps only the fields Claude Code reads, normalising other tools' spellings.
func serverFrom(name string, m map[string]any) (Server, []string, error) {
	var notes []string
	s := Server{Name: name, Type: strings.ToLower(str(m["type"]))}
	switch s.Type {
	case "streamable-http", "streamablehttp", "http":
		s.Type = "http"
	case "sse":
	case "", "stdio":
		s.Type = "stdio"
		if _, ok := m["url"].(string); ok && str(m["command"]) == "" {
			s.Type = "http"
		}
	default:
		return s, nil, fmt.Errorf("transport %q isn't supported (use stdio, http or sse)", s.Type)
	}
	s.Command, s.URL = str(m["command"]), str(m["url"])
	if a, ok := m["args"].([]any); ok {
		for _, x := range a {
			s.Args = append(s.Args, scalar(x))
		}
	}
	s.Env = strMap(m["env"])
	s.Headers = strMap(m["headers"])
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains([]string{"type", "command", "args", "env", "url", "headers"}, k) {
			notes = append(notes, fmt.Sprintf("%s: dropped %q, which Claude Code doesn't use", orName(name), k))
		}
	}
	// VS Code prompts for ${input:x}; Claude Code reads environment variables instead.
	fix := func(v string) string {
		v = vscodeEnvRef.ReplaceAllString(v, "$${$1}")
		return vscodeInput.ReplaceAllStringFunc(v, func(in string) string {
			id := vscodeInput.FindStringSubmatch(in)[1]
			env := envName(id)
			notes = append(notes, fmt.Sprintf("%s: ${input:%s} became ${%s}; set %s in your environment", orName(name), id, env, env))
			return "${" + env + "}"
		})
	}
	for k, v := range s.Env {
		s.Env[k] = fix(v)
	}
	for k, v := range s.Headers {
		s.Headers[k] = fix(v)
	}
	for i := range s.Args {
		s.Args[i] = fix(s.Args[i])
	}
	s.URL = fix(s.URL)
	return s, notes, nil
}

// stripJSONC drops // and /* */ comments and trailing commas outside strings, so snippets
// copied from docs that use JSONC still parse.
func stripJSONC(s string) string {
	var b strings.Builder
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			b.WriteByte(c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 3
		case c == ',':
			j := i + 1
			for j < len(s) && strings.ContainsRune(" \t\r\n", rune(s[j])) {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue // trailing comma
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ---------- commands: `claude mcp add`, `/plugin marketplace add`, `/plugin install` ----------

func parseCommands(text string) (Plan, error) {
	var p Plan
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		w, err := words(line)
		if err != nil {
			return Plan{}, err
		}
		if len(w) > 0 && w[0] == "claude" {
			w = w[1:]
		} else if len(w) > 0 && strings.HasPrefix(w[0], "/") { // a slash command typed in Claude Code
			w[0] = strings.TrimPrefix(w[0], "/")
		}
		switch {
		case len(w) >= 4 && w[0] == "plugin" && w[1] == "marketplace" && w[2] == "add":
			p.Kind, p.Marketplace = "plugin", lastPositional(w[3:])
		case len(w) >= 3 && w[0] == "plugin" && (w[1] == "install" || w[1] == "i"):
			p.Kind, p.Plugin = "plugin", lastPositional(w[2:])
		case len(w) >= 3 && w[0] == "mcp" && w[1] == "add-json":
			pos := positionals(w[2:])
			if len(pos) != 2 {
				return Plan{}, errors.New("`claude mcp add-json` needs a name and the JSON")
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(pos[1]), &m); err != nil {
				return Plan{}, fmt.Errorf("the JSON in that command isn't valid: %v", err)
			}
			s, notes, err := serverFrom(pos[0], m)
			if err != nil {
				return Plan{}, err
			}
			p.Kind, p.Servers, p.Notes = "mcp", append(p.Servers, s), append(p.Notes, notes...)
		case len(w) >= 3 && w[0] == "mcp" && w[1] == "add":
			s, err := mcpAddArgs(w[2:])
			if err != nil {
				return Plan{}, err
			}
			p.Kind, p.Servers = "mcp", append(p.Servers, s)
		default:
			return Plan{}, fmt.Errorf("not something cca can add: %q", line)
		}
	}
	if p.Kind == "" {
		return Plan{}, errors.New("nothing to add in that text")
	}
	return p, nil
}

// mcpAddArgs reads `claude mcp add [options] <name> <commandOrUrl> [args...]`.
func mcpAddArgs(w []string) (Server, error) {
	s := Server{Type: "stdio", Env: map[string]string{}, Headers: map[string]string{}}
	var pos []string
	for i := 0; i < len(w); i++ {
		a := w[i]
		next := func() string {
			if i+1 < len(w) {
				i++
				return w[i]
			}
			return ""
		}
		switch {
		case a == "--":
			pos = append(pos, w[i+1:]...)
			i = len(w)
		case a == "-t" || a == "--transport":
			s.Type = strings.ToLower(next())
		case a == "-s" || a == "--scope":
			next() // the scope is chosen in the app
		case a == "-e" || a == "--env":
			for i+1 < len(w) && strings.Contains(w[i+1], "=") && !strings.HasPrefix(w[i+1], "-") {
				k, v, _ := strings.Cut(next(), "=")
				s.Env[k] = v
			}
		case a == "-H" || a == "--header":
			for i+1 < len(w) && strings.Contains(w[i+1], ":") && !strings.HasPrefix(w[i+1], "-") {
				k, v, _ := strings.Cut(next(), ":")
				s.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		case strings.HasPrefix(a, "-") && len(pos) < 2:
			return s, fmt.Errorf("option %s isn't supported here; paste the server's JSON instead", a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) < 2 {
		return s, errors.New("`claude mcp add` needs a name and a command or URL")
	}
	s.Name = pos[0]
	if s.Type == "streamable-http" {
		s.Type = "http"
	}
	if s.Type == "stdio" {
		s.Command, s.Args = pos[1], pos[2:]
	} else {
		s.URL = pos[1]
	}
	if len(s.Env) == 0 {
		s.Env = nil
	}
	if len(s.Headers) == 0 {
		s.Headers = nil
	}
	return s, nil
}

// words splits a command line like a POSIX shell would for quotes and backslashes, without
// expanding anything. Nothing it returns is ever run by a shell.
func words(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, quote, esc := false, byte(0), false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case esc:
			cur.WriteByte(c)
			esc = false
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' {
				esc = true
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\':
			esc, in = true, true
		case c == ' ' || c == '\t':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("a quote in that command isn't closed")
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

func positionals(w []string) []string {
	var out []string
	for i := 0; i < len(w); i++ {
		switch {
		case w[i] == "-s" || w[i] == "--scope" || w[i] == "--sparse" || w[i] == "--config":
			i++
		case strings.HasPrefix(w[i], "-"):
		default:
			out = append(out, w[i])
		}
	}
	return out
}

func lastPositional(w []string) string {
	p := positionals(w)
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

// ---------- SKILL.md ----------

func parseSkill(text string) (Plan, error) {
	head, body, ok := strings.Cut(strings.TrimPrefix(text, "---"), "\n---")
	if !ok {
		return Plan{}, errors.New("that SKILL.md header isn't closed with ---")
	}
	sk := &Skill{Body: strings.TrimLeft(strings.TrimPrefix(body, "\n"), "\n")}
	for _, ln := range strings.Split(head, "\n") {
		k, v, _ := strings.Cut(ln, ":")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			sk.Name = v
		case "description":
			sk.Description = v
		}
	}
	return Plan{Kind: "skill", Skill: sk}, nil
}

// ---------- validation, right before anything runs ----------

// Scopes Claude Code accepts for MCP servers and plugins.
var Scopes = []string{"user", "project", "local"}

// CheckServer validates a server for a scope. Project scope writes the repo's .mcp.json, which
// is usually committed, so secret-looking values there must be ${VAR} references.
func CheckServer(s Server, scope string) error {
	if !nameRe.MatchString(s.Name) {
		return errors.New("a server name can use only letters, numbers, - and _ (up to 64)")
	}
	if !slices.Contains(Scopes, scope) {
		return fmt.Errorf("unknown scope %q", scope)
	}
	switch s.Type {
	case "stdio":
		if strings.TrimSpace(s.Command) == "" || s.URL != "" || len(s.Headers) > 0 {
			return errors.New("a stdio server needs a command, and no url or headers")
		}
	case "http", "sse":
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("a remote server needs an http or https url")
		}
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return errors.New("a remote server takes a url and headers, not a command")
		}
	default:
		return fmt.Errorf("transport %q isn't supported", s.Type)
	}
	// In every scope: `claude mcp add-json` only takes the definition as an argument, which other
	// programs (and endpoint security logs) can read, and project scope would commit it too.
	if m := s.InlineSecret(); m != "" {
		return fmt.Errorf("%s holds a key written out in full: move it to an environment variable and refer to it as ${NAME}, so it never sits in a config file or a command line", m)
	}
	if l := s.Literals(); len(l) > 0 {
		return fmt.Errorf("%s is written out in full: use ${%s} instead and set %s in your shell profile, so the key never sits in a config file or a command line", l[0], envName(l[0]), envName(l[0]))
	}
	return nil
}

// JSON is the exact definition handed to `claude mcp add-json`.
func (s Server) JSON() string {
	m := map[string]any{"type": s.Type}
	if s.Type == "stdio" {
		m["command"] = s.Command
		if len(s.Args) > 0 {
			m["args"] = s.Args
		}
		if len(s.Env) > 0 {
			m["env"] = s.Env
		}
	} else {
		m["url"] = s.URL
		if len(s.Headers) > 0 {
			m["headers"] = s.Headers
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// Literals lists env and header names whose values are written out as plain text rather than
// ${VAR} references, so the app can warn before saving them.
func (s Server) Literals() []string {
	var out []string
	for k, v := range s.Env {
		if secretKeyRe.MatchString(k) && v != "" && !refRe.MatchString(v) {
			out = append(out, k)
		}
	}
	for k, v := range s.Headers {
		// The value must be exactly ${VAR}, or a scheme and ${VAR}: "Bearer sk-1 ${X}" still holds a key.
		if (secretKeyRe.MatchString(k) || authScheme.MatchString(v)) && !refRe.MatchString(authScheme.ReplaceAllString(v, "")) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// InlineSecret names a key written out somewhere other than env or headers: in the arguments
// (--api-key sk-…, --token=…, or a known key format) or the URL (user:password@, ?api_key=…).
// Those reach the add-json command line just the same. It returns "" when there is none.
func (s Server) InlineSecret() string {
	for i, a := range s.Args {
		k, v, eq := strings.Cut(a, "=")
		if tokenLike.MatchString(a) || envAssign.MatchString(a) {
			return "an argument"
		}
		if !strings.HasPrefix(k, "-") || !argKeyRe.MatchString(k) {
			continue
		}
		if !eq && i+1 < len(s.Args) {
			v = s.Args[i+1]
		}
		if v != "" && !refRe.MatchString(v) { // only exactly ${VAR} is clean
			return "the " + k + " argument"
		}
	}
	if u, err := url.Parse(strings.TrimSpace(s.URL)); err == nil {
		if u.User != nil {
			return "the URL's user:password"
		}
		for _, seg := range strings.Split(u.Path, "/") { // e.g. https://mcp.example.com/s/<secret>/mcp
			if len(seg) >= 20 && keyish.MatchString(seg) {
				return "the URL's path"
			}
		}
		for k, vs := range u.Query() {
			for _, v := range vs {
				if (secretKeyRe.MatchString(k) || tokenLike.MatchString(v) || len(v) >= 16 && keyish.MatchString(v)) && !strings.Contains(v, "${") {
					return "the URL's " + k + " parameter"
				}
			}
		}
	}
	return ""
}

// CheckPlugin validates a marketplace source and a plugin id.
func CheckPlugin(marketplace, plugin string, localDir func(string) bool) error {
	if plugin != "" && !pluginRe.MatchString(plugin) {
		return errors.New("a plugin is named like name@marketplace")
	}
	if marketplace == "" {
		if plugin == "" {
			return errors.New("nothing to install")
		}
		return nil
	}
	if repoRe.MatchString(marketplace) {
		return nil
	}
	if u, err := url.Parse(marketplace); err == nil && u.Scheme == "https" && u.Host != "" {
		return nil
	}
	if strings.HasPrefix(marketplace, "/") && localDir(marketplace) {
		return nil
	}
	return errors.New("a marketplace is owner/repo, an https:// link, or a folder on this Mac")
}

// CheckSkill validates a new skill.
func CheckSkill(sk Skill) error {
	if !skillNameRe.MatchString(sk.Name) || sk.Name == "synced" {
		return errors.New("a skill name uses lowercase letters, numbers and hyphens (up to 64)")
	}
	if strings.TrimSpace(sk.Description) == "" {
		return errors.New("a skill needs a description: Claude reads it to decide when to use the skill")
	}
	if len(sk.Description) > 1536 {
		return errors.New("keep the description under 1,536 characters")
	}
	return nil
}

// SkillFile is the SKILL.md for sk.
func SkillFile(sk Skill) string {
	desc, _ := json.Marshal(strings.TrimSpace(sk.Description)) // a JSON string is valid YAML
	return "---\nname: " + sk.Name + "\ndescription: " + string(desc) + "\n---\n\n" + strings.TrimSpace(sk.Body) + "\n"
}

// ---------- small helpers ----------

func isMap(v any) bool { _, ok := v.(map[string]any); return ok }
func str(v any) string { s, _ := v.(string); return s }

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func strMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, x := range m {
		out[k] = scalar(x)
	}
	return out
}

func orName(n string) string {
	if n == "" {
		return "server"
	}
	return n
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

func envName(k string) string { return strings.ToUpper(nonAlnum.ReplaceAllString(k, "_")) }
