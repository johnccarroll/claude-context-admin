// Package mcpserver is `cca mcp`: a stdio MCP server that lets Claude see the whole picture of
// what it loads and suggest cleanups. It never changes files itself; suggestions wait in
// Claude Context Admin's Review until the user accepts them.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnccarroll/claude-context-admin/internal/audit"
	"github.com/johnccarroll/claude-context-admin/internal/entry"
	"github.com/johnccarroll/claude-context-admin/internal/loads"
	"github.com/johnccarroll/claude-context-admin/internal/proposal"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/write"
)

// Run serves MCP over stdin/stdout until ctx ends. run calls the claude CLI (for plugin costs).
func Run(ctx context.Context, home, version string, run scan.Runner) error {
	return New(home, version, run).Run(ctx, &mcp.StdioTransport{})
}

// New builds the server and its tools.
func New(home, version string, run scan.Runner) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "claude-context-admin", Title: "Claude Context Admin", Version: version}, nil)
	inventory := func(ctx context.Context, costs bool) *scan.Inventory {
		return (&scan.Scanner{Home: home, Run: run, Costs: costs}).Scan(ctx)
	}

	type searchIn struct {
		Query   string `json:"query" jsonschema:"words to find, case-insensitive, in memory titles, summaries and full text"`
		Project string `json:"project,omitempty" jsonschema:"absolute project path to limit results to; omit to search every project and global memory"`
	}
	type memHit struct {
		Path        string `json:"path"`
		Name        string `json:"name"`
		Type        string `json:"type,omitempty"`
		Project     string `json:"project,omitempty"`
		Description string `json:"description,omitempty"`
		Snippet     string `json:"snippet,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "memory_search", Description: "Search Claude Code's auto-memory across every project and the global memory folder. " +
		"Matches titles, one-line summaries and full text, and returns each memory's path, name, type, project, summary and a snippet around the match. " +
		"Use it to check what is already remembered before suggesting a new memory, or to find duplicates and stale notes to suggest cleaning up. " +
		"It returns at most 40 results and never changes anything."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, []memHit, error) {
			inv := inventory(ctx, false)
			q := strings.ToLower(in.Query)
			out := []memHit{}
			for _, e := range inv.Entries {
				if e.Kind != entry.Memory || (in.Project != "" && e.Project != in.Project) || len(out) >= 40 {
					continue
				}
				b, _ := scan.ReadText(e.Path)
				text := strings.ToLower(e.Name + " " + e.Description + " " + string(b))
				if q == "" || strings.Contains(text, q) {
					out = append(out, memHit{e.Path, e.Name, e.Type, e.Project, e.Description, scan.Snippet(string(b), q)})
				}
			}
			return nil, out, nil
		})

	type readIn struct {
		Path string `json:"path" jsonschema:"absolute path of a memory, CLAUDE.md, AGENTS.md, rule, skill, command or agent file, as returned by the other tools"`
	}
	type readOut struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "memory_read", Description: "Read the full text of one memory or instruction file (memory, CLAUDE.md, AGENTS.md, rule, skill, command or agent). " +
		"Only files Claude Code actually loads can be read; settings and MCP configuration (which can hold secrets) cannot. Never changes anything."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, readOut, error) {
			inv := inventory(ctx, false)
			readable := []entry.Kind{entry.Memory, entry.MemoryIndex, entry.Instructions, entry.Rule, entry.Skill, entry.Command, entry.Agent}
			for _, e := range inv.Entries {
				// Checked again now, through symlinks: the file may have changed since the scan.
				if e.Path == in.Path && slices.Contains(readable, e.Kind) && scan.IsMarkdown(e.Path) {
					b, err := scan.ReadText(e.Path)
					return nil, readOut{e.Path, string(b)}, err
				}
			}
			return nil, readOut{}, fmt.Errorf("%s is not a memory or instruction file Claude Code loads", in.Path)
		})

	type projectIn struct {
		Project string `json:"project" jsonschema:"absolute path of the project folder (the folder a session starts in)"`
	}
	type budgetOut struct {
		Budget  loads.Budget   `json:"budget"`
		Shadows []loads.Shadow `json:"shadows"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "context_budget", Description: "Estimate what Claude Code loads at the start of a session in a project, before the first message: " +
		"user and project instructions with their imports, the memory index (against its ~200-line / 25 KB limit), plugin listings, skills, commands and agents, and MCP servers. " +
		"Also lists names defined in more than one scope and which one wins. Token counts are estimates (bytes / 4), good for ranking, not billing. Never changes anything."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in projectIn) (*mcp.CallToolResult, budgetOut, error) {
			inv := inventory(ctx, true)
			sh := loads.Shadows(inv, in.Project)
			for i := range sh { // names and places only: Meta can hold URLs and command lines
				sh[i].Winner.Meta = nil
				for j := range sh[i].Hidden {
					sh[i].Hidden[j].Meta = nil
				}
			}
			return nil, budgetOut{loads.Compute(inv, in.Project), sh}, nil
		})

	type toolkitIn struct {
		Project string `json:"project,omitempty" jsonschema:"absolute project path; omit for everything"`
	}
	type item struct {
		Kind    entry.Kind     `json:"kind"`
		Name    string         `json:"name"`
		Scope   entry.Scope    `json:"scope"`
		Project string         `json:"project,omitempty"`
		Enabled bool           `json:"enabled"`
		Path    string         `json:"path,omitempty"`
		Detail  map[string]any `json:"detail,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "toolkit_list", Description: "List plugins (with enabled state and always-on token cost), MCP servers (transport and the names of their env keys, never the values), " +
		"skills, commands, agents and hooks, at user, project, local and plugin scope. Use it with health_findings and context_budget to find what to turn off or consolidate. Never changes anything."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in toolkitIn) (*mcp.CallToolResult, []item, error) {
			inv := inventory(ctx, true)
			keep := map[entry.Kind][]string{entry.Plugin: {"id", "alwaysOnTokens", "components"}, entry.MCP: {"transport", "envKeys", "url"},
				entry.Hook: {"event", "matcher"}, entry.Skill: {"plugin"}, entry.Command: {"plugin"}, entry.Agent: {"plugin"}}
			out := []item{}
			for _, e := range inv.Entries {
				fields, ok := keep[e.Kind]
				if !ok || (in.Project != "" && e.Project != "" && e.Project != in.Project) {
					continue
				}
				d := map[string]any{}
				for _, f := range fields {
					if v, ok := e.Meta[f]; ok {
						d[f] = v
					}
				}
				out = append(out, item{e.Kind, e.Name, e.Scope, e.Project, e.Enabled, e.Path, d})
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "health_findings", Description: "Problems found in memories, instructions and the toolkit: broken [[links]], copies of the same skill in several projects, " +
		"hooks pointing at missing scripts, plugins installed twice, all-caps emphasis, prompting written for older models, retired model names, oversized skills, " +
		"an AGENTS.md that Claude ignores, memories never opened in 90 days, and more. Each finding has a code, the file, and for instruction checks a confidence and the exact lines. " +
		"Use it to decide what to suggest with propose_change. Never changes anything."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in toolkitIn) (*mcp.CallToolResult, []audit.Finding, error) {
			inv := inventory(ctx, true)
			out := []audit.Finding{}
			for _, f := range audit.Run(inv, nil, time.Now()).Findings {
				if in.Project == "" || f.Project == "" || f.Project == in.Project {
					out = append(out, f)
				}
			}
			return nil, out, nil
		})

	type proposeIn struct {
		Op     string         `json:"op" jsonschema:"the change to suggest; one of the ops listed in this tool's description"`
		Args   map[string]any `json:"args" jsonschema:"arguments for the op, e.g. {\"path\": \"/abs/memory.md\"}; paths must come from the other tools"`
		Reason string         `json:"reason" jsonschema:"one or two sentences the user reads when deciding: what is wrong and why this change fixes it"`
	}
	type proposeOut struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "propose_change", Description: "Suggest a change to Claude Code's memory or setup. Nothing changes now: the suggestion appears in Claude Context Admin's Review, " +
		"where the user accepts it (the app validates and applies it, undoably) or dismisses it. Prefer this over editing memory or config files directly when the user asked for cleanup or review. " +
		"Ops and their args: memory-save {path, name?, type?, description, body} (name only to rename); memory-create {dir, title, type, description, body}; " +
		"memory-trash {path}; memory-promote {path} (make global); relink {from, to, paths}; unlink {target, paths}; fix-frontmatter {path}; merge-global {keep, drop}; " +
		"remove-hooks {script}; quiet-caps {path} (rewrite all-caps emphasis at normal volume); " +
		"convert-to-agents {path} (a project CLAUDE.md); plugin-enable / plugin-disable / plugin-uninstall {id}; mcp-remove {name, scope, project}; " +
		"bulk {action: trash|global|move|type, paths, dir?, type?}; project-relocate {from, to} (memory folder of a project whose folder is gone, and the folder it moved to). Memory types: feedback, project, reference, user."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in proposeIn) (*mcp.CallToolResult, proposeOut, error) {
			if !slices.Contains(proposal.Ops, in.Op) { // the app validates their arguments on accept
				return nil, proposeOut{}, fmt.Errorf("unknown op %q; use one of: %s", in.Op, strings.Join(proposal.Ops, ", "))
			}
			if b, _ := json.Marshal(in.Args); len(b) > 256<<10 || len(in.Reason) > 2000 {
				return nil, proposeOut{}, fmt.Errorf("that suggestion is too large: keep the reason under 2,000 characters and the arguments under 256 KB")
			}
			if strings.TrimSpace(in.Reason) == "" {
				return nil, proposeOut{}, fmt.Errorf("give a reason the user can read when deciding")
			}
			p, err := proposal.Add(write.DataDir(home), in.Op, in.Args, in.Reason)
			if err != nil {
				return nil, proposeOut{}, err
			}
			return nil, proposeOut{p.ID, "Suggested. The user will see it in Claude Context Admin under Review and can accept or dismiss it."}, nil
		})

	return s
}
