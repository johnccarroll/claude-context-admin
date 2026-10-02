package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/johnccarroll/claude-context-admin/internal/entry"
)

type pluginListItem struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	InstallPath string `json:"installPath"`
}

// plugins lists plugins through the official CLI, then reads each plugin's own files for the
// skills, commands, agents, hooks and MCP servers it ships.
func (s *Scanner) plugins(ctx context.Context, inv *Inventory) {
	out, err := s.pluginList(ctx)
	if err != nil {
		inv.warn("claude plugin list failed: " + err.Error())
		return
	}
	var items []pluginListItem
	if err := json.Unmarshal(LastJSON(out), &items); err != nil {
		inv.warn("could not parse claude plugin list output: " + err.Error())
		return
	}
	details := map[string]pluginDetails{}
	if s.Costs {
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for _, it := range items {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				if strings.HasPrefix(it.ID, "-") { // never an argument claude could read as a flag
					return
				}
				b, err := s.Run(ctx, "plugin", "details", it.ID)
				if err != nil {
					return
				}
				d := parseDetails(string(b))
				mu.Lock()
				details[it.ID] = d
				mu.Unlock()
			})
		}
		wg.Wait()
	}
	byName := map[string]int{}
	for _, it := range items {
		name, market, _ := strings.Cut(it.ID, "@")
		meta := map[string]any{"id": it.ID, "marketplace": market, "installScope": it.Scope}
		e := entry.Entry{Kind: entry.Plugin, Scope: entry.ScopeUser, Path: it.InstallPath, Name: name,
			Enabled: it.Enabled, Meta: meta}
		if d, ok := details[it.ID]; ok {
			e.Description = d.Description
			meta["components"] = d.Components
			meta["alwaysOnTokens"] = d.AlwaysOn
			meta["perComponent"] = d.Per
		}
		if n, dup := byName[name]; dup {
			inv.Entries[n].Issues = append(inv.Entries[n].Issues, "plugin-installed-twice")
			e.Issues = append(e.Issues, "plugin-installed-twice")
		}
		inv.Entries = append(inv.Entries, e)
		byName[name] = len(inv.Entries) - 1
		if it.InstallPath != "" {
			pm := map[string]any{"plugin": name, "pluginEnabled": it.Enabled}
			s.toolkitDir(inv, it.InstallPath, "", entry.ScopePlugin, pm)
			s.hookFile(inv, filepath.Join(it.InstallPath, "hooks", "hooks.json"), "", entry.ScopePlugin, pm)
		}
	}
}

// LastJSON returns the JSON payload: the whole output, or else its last JSON line
// (the CLI may print notices before a one-line result).
func LastJSON(b []byte) []byte {
	if json.Valid(bytes.TrimSpace(b)) {
		return b
	}
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if json.Valid(lines[i]) {
			return lines[i]
		}
	}
	return b
}

type pluginDetails struct {
	Description string            `json:"description"`
	Components  map[string]int    `json:"components"`
	AlwaysOn    int               `json:"alwaysOn"`
	Per         map[string][2]int `json:"perComponent"` // name -> [always-on, on-invoke]
}

var (
	descRe   = regexp.MustCompile(`(?m)^\s*Description:\s*(.+)$`)
	compRe   = regexp.MustCompile(`(?m)^\s+(Skills|Agents|Hooks|MCP servers|LSP servers) \((\d+)\)`)
	alwaysRe = regexp.MustCompile(`Always-on:\s*~?([\d,.]+k?)`)
	perRe    = regexp.MustCompile(`(?m)^\s{2}(\S+)\s+~?([\d,.]+k?)\s+~?([\d,.]+k?)\s*$`)
)

// parseDetails reads `claude plugin details` text output (it has no --json mode).
func parseDetails(s string) pluginDetails {
	d := pluginDetails{Components: map[string]int{}, Per: map[string][2]int{}}
	if m := descRe.FindStringSubmatch(s); m != nil {
		d.Description = strings.TrimSpace(m[1])
	}
	for _, m := range compRe.FindAllStringSubmatch(s, -1) {
		d.Components[m[1]], _ = strconv.Atoi(m[2])
	}
	if m := alwaysRe.FindStringSubmatch(s); m != nil {
		d.AlwaysOn = tokens(m[1])
	}
	if i := strings.Index(s, "Per-component"); i >= 0 {
		for _, m := range perRe.FindAllStringSubmatch(s[i:], -1) {
			if m[1] == "component" {
				continue
			}
			d.Per[m[1]] = [2]int{tokens(m[2]), tokens(m[3])}
		}
	}
	return d
}

// tokens parses "~1,288", "4.2k" or "140" into a token count.
func tokens(s string) int {
	s = strings.ReplaceAll(strings.TrimPrefix(s, "~"), ",", "")
	mult := 1.0
	if strings.HasSuffix(s, "k") {
		mult, s = 1000, strings.TrimSuffix(s, "k")
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int(f * mult)
}

// pluginList runs `claude plugin list --json`, or reuses the last output while the plugin
// registry and the user settings (which enable plugins) are unchanged.
func (s *Scanner) pluginList(ctx context.Context) ([]byte, error) {
	if s.Plugins == nil {
		return s.Run(ctx, "plugin", "list", "--json")
	}
	dir := ConfigDir(s.Home)
	var key strings.Builder
	for _, f := range []string{"plugins/installed_plugins.json", "plugins/known_marketplaces.json", "settings.json", "settings.local.json"} {
		if fi, err := os.Stat(filepath.Join(dir, f)); err == nil {
			fmt.Fprintf(&key, "%s %d %d;", f, fi.ModTime().UnixNano(), fi.Size())
		}
	}
	c := s.Plugins
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == key.String() && c.out != "" {
		return []byte(c.out), nil
	}
	out, err := s.Run(ctx, "plugin", "list", "--json")
	if err == nil {
		c.key, c.out = key.String(), string(out)
	}
	return out, err
}
