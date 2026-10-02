package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

// toolkit scans skills, commands and agents at user and project scope.
func (s *Scanner) toolkit(inv *Inventory) {
	s.toolkitDir(inv, s.claudeDir(), "", entry.ScopeUser, nil)
	for _, r := range inv.repos() {
		s.toolkitDir(inv, filepath.Join(r.Path, ".claude"), r.Path, entry.ScopeProject, nil)
	}
}

// toolkitDir reads skills/, commands/ and agents/ under root. Plugins reuse it with their own meta.
func (s *Scanner) toolkitDir(inv *Inventory, root, project string, scope entry.Scope, meta map[string]any) {
	skills, _ := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	for _, p := range skills {
		s.mdEntry(inv, p, filepath.Base(filepath.Dir(p)), entry.Skill, project, scope, meta)
	}
	cmdRoot := filepath.Join(root, "commands")
	for _, p := range globMD(cmdRoot) {
		rel := strings.TrimSuffix(strings.TrimPrefix(p, cmdRoot+string(os.PathSeparator)), ".md")
		s.mdEntry(inv, p, strings.ReplaceAll(rel, string(os.PathSeparator), ":"), entry.Command, project, scope, meta)
	}
	agents, _ := filepath.Glob(filepath.Join(root, "agents", "*.md"))
	for _, p := range agents {
		s.mdEntry(inv, p, strings.TrimSuffix(filepath.Base(p), ".md"), entry.Agent, project, scope, meta)
	}
}

func (s *Scanner) mdEntry(inv *Inventory, path, fallback string, kind entry.Kind, project string, scope entry.Scope, meta map[string]any) {
	src, fi, ok := readFile(path)
	if !ok {
		return
	}
	d := parseDoc(src)
	name := d.fm.Name
	if name == "" || kind == entry.Command {
		name = fallback
	}
	inv.Entries = append(inv.Entries, entry.Entry{Kind: kind, Scope: scope, Project: project, Path: path,
		Name: name, Description: d.fm.Description, Enabled: true, Bytes: fi.Size(), Lines: d.lines, Modified: fi.ModTime().UTC().Format(time.RFC3339), Meta: maps.Clone(meta)})
}

// mcpServer keeps only what the UI may show. Env and header VALUES are read into RawMessage and
// dropped; only their key names survive.
type mcpServer struct {
	Type    string                     `json:"type"`
	Command string                     `json:"command"`
	URL     string                     `json:"url"`
	Env     map[string]json.RawMessage `json:"env"`
	Headers map[string]json.RawMessage `json:"headers"`
}

// mcp reads servers from ~/.claude.json (user + local scope) and each repo's .mcp.json (project scope).
// ~/.claude.json is never written by this tool; Claude Code rewrites it constantly.
func (s *Scanner) mcp(inv *Inventory) {
	var cfg struct {
		MCPServers map[string]mcpServer `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers         map[string]mcpServer `json:"mcpServers"`
			DisabledMCPServers []string             `json:"disabledMcpServers"`
		} `json:"projects"`
	}
	if b, err := os.ReadFile(GlobalConfig(s.Home)); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			inv.warn("could not parse ~/.claude.json: " + err.Error())
		}
	}
	add := func(name string, srv mcpServer, scope entry.Scope, project, source string, disabled bool) {
		inv.Entries = append(inv.Entries, entry.Entry{Kind: entry.MCP, Scope: scope, Project: project, Path: source,
			Name: name, Enabled: !disabled, Meta: mcpMeta(srv)})
	}
	for _, n := range slices.Sorted(maps.Keys(cfg.MCPServers)) {
		add(n, cfg.MCPServers[n], entry.ScopeUser, "", GlobalConfig(s.Home), false)
	}
	for _, proj := range slices.Sorted(maps.Keys(cfg.Projects)) {
		pc := cfg.Projects[proj]
		for _, n := range slices.Sorted(maps.Keys(pc.MCPServers)) {
			add(n, pc.MCPServers[n], entry.ScopeLocal, proj, GlobalConfig(s.Home), slices.Contains(pc.DisabledMCPServers, n))
		}
	}
	for _, r := range inv.repos() {
		var pj struct {
			MCPServers map[string]mcpServer `json:"mcpServers"`
		}
		p := filepath.Join(r.Path, ".mcp.json")
		b, err := ReadText(p) // a repo's file: capped, regular files only (not /dev/zero)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(b, &pj); err != nil {
			inv.warn("could not parse " + p + ": " + err.Error())
			continue
		}
		disabled := cfg.Projects[r.Path].DisabledMCPServers
		for _, n := range slices.Sorted(maps.Keys(pj.MCPServers)) {
			add(n, pj.MCPServers[n], entry.ScopeProject, r.Path, p, slices.Contains(disabled, n))
		}
	}
}

func mcpMeta(srv mcpServer) map[string]any {
	transport := srv.Type
	if transport == "" {
		transport = "stdio"
		if srv.URL != "" {
			transport = "http"
		}
	}
	m := map[string]any{"transport": transport, "envKeys": slices.Sorted(maps.Keys(srv.Env))}
	if srv.URL != "" {
		// user:password@ first (a password can contain ? or #), then the query, which can carry tokens.
		u := userinfo.ReplaceAllString(strings.TrimSpace(srv.URL), "$1")
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			u = u[:i]
		}
		m["url"] = redactPath(u)
	}
	if srv.Command != "" {
		m["command"] = filepath.Base(srv.Command)
	}
	return m
}

type hookSettings struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// hooks reads every settings layer we can see: user, project and local.
func (s *Scanner) hooks(inv *Inventory) {
	s.hookFile(inv, filepath.Join(s.claudeDir(), "settings.json"), "", entry.ScopeUser, nil)
	s.hookFile(inv, filepath.Join(s.claudeDir(), "settings.local.json"), "", entry.ScopeUser, nil)
	for _, r := range inv.repos() {
		s.hookFile(inv, filepath.Join(r.Path, ".claude", "settings.json"), r.Path, entry.ScopeProject, nil)
		s.hookFile(inv, filepath.Join(r.Path, ".claude", "settings.local.json"), r.Path, entry.ScopeLocal, nil)
	}
}

func (s *Scanner) hookFile(inv *Inventory, path, project string, scope entry.Scope, meta map[string]any) {
	b, err := ReadText(path)
	if err != nil {
		return
	}
	var hs hookSettings
	if err := json.Unmarshal(b, &hs); err != nil {
		inv.warn("could not parse " + path + ": " + err.Error())
		return
	}
	for _, ev := range slices.Sorted(maps.Keys(hs.Hooks)) {
		for gi, m := range hs.Hooks[ev] {
			for hi, h := range m.Hooks {
				e := entry.Entry{Kind: entry.Hook, Scope: scope, Project: project, Path: path, Name: ev, Enabled: true,
					Meta: maps.Clone(meta)}
				e.Meta = setMeta(e.Meta, "event", ev)
				e.Meta["matcher"] = m.Matcher
				e.Meta["group"], e.Meta["pos"] = gi, hi // exact position, for removal
				e.Meta["command"] = redact(strings.ReplaceAll(h.Command, s.Home, "~"))
				for _, sp := range scriptPaths(h.Command) {
					if _, err := os.Stat(s.expand(strings.ReplaceAll(sp, "$HOME", "~"), s.Home)); err != nil {
						e.Issues = append(e.Issues, "hook-script-missing:"+sp)
					}
				}
				inv.Entries = append(inv.Entries, e)
			}
		}
	}
}

var scriptRe = regexp.MustCompile(`(?:~|\$HOME|/Users/[^/\s"']+)/[^\s"'|;&]+\.(?:sh|mjs|js|cjs|ts|py|rb)`)

func scriptPaths(cmd string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range scriptRe.FindAllString(cmd, -1) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

var secretRe = regexp.MustCompile(`(?i)(bearer\s+)[^\s"']+|((?:token|key|secret|password)=)[^\s"'&]+|\b[A-Za-z0-9_\-]{32,}\b`)

// redact masks anything in a command line that looks like a credential.
func redact(s string) string {
	return secretRe.ReplaceAllStringFunc(s, func(m string) string {
		if sm := secretRe.FindStringSubmatch(m); sm[1] != "" {
			return sm[1] + "••••"
		} else if sm[2] != "" {
			return sm[2] + "••••"
		}
		return "••••"
	})
}

// redactPath hides URL path segments that look like credentials (long random-looking strings),
// e.g. https://mcp.example.com/s/<token>/mcp.
func redactPath(u string) string {
	parts := strings.Split(u, "/")
	for i := 3; i < len(parts); i++ { // after scheme://host
		if p := parts[i]; len(p) >= 20 && tokenish.MatchString(p) {
			parts[i] = "••••"
		}
	}
	return strings.Join(parts, "/")
}

var userinfo = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*://)[^/]*@`)

var tokenish = regexp.MustCompile(`^[A-Za-z0-9_\-.~=+]*[0-9][A-Za-z0-9_\-.~=+]*$`)

// MCPStamp is a digest of the parts of the global config the scan reads (MCP servers per scope),
// so a rescan can be skipped when Claude Code rewrites the file for anything else.
func MCPStamp(home string) string {
	b, err := os.ReadFile(GlobalConfig(home))
	if err != nil {
		return ""
	}
	var cfg struct {
		MCPServers json.RawMessage `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers json.RawMessage `json:"mcpServers"`
			Disabled   json.RawMessage `json:"disabledMcpServers"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &cfg) == nil {
		b, _ = json.Marshal(cfg) // map keys sorted, so equal content gives an equal stamp
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
