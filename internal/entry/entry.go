// Package entry defines the one model every scanner, view, audit and MCP tool shares.
package entry

// Kind is what an entry is.
type Kind string

const (
	Memory       Kind = "memory"
	MemoryIndex  Kind = "memory-index"
	Instructions Kind = "instructions"
	Rule         Kind = "rule"
	Skill        Kind = "skill"
	Command      Kind = "command"
	Agent        Kind = "agent"
	Plugin       Kind = "plugin"
	MCP          Kind = "mcp"
	Hook         Kind = "hook"
)

// Scope is where an entry applies.
type Scope string

const (
	ScopeUser    Scope = "user"    // everywhere
	ScopeProject Scope = "project" // committed to a repo
	ScopeLocal   Scope = "local"   // this user, one project
	ScopePlugin  Scope = "plugin"  // shipped by a plugin
	ScopeImport  Scope = "import"  // pulled in by an @import
)

// Entry is one thing Claude Code can load.
type Entry struct {
	Kind        Kind           `json:"kind"`
	Scope       Scope          `json:"scope"`
	Project     string         `json:"project,omitempty"` // absolute repo path; empty for user scope
	Path        string         `json:"path,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type,omitempty"` // memory type: feedback|project|reference|user
	Enabled     bool           `json:"enabled"`
	Bytes       int64          `json:"bytes,omitempty"`
	Lines       int            `json:"lines,omitempty"`
	Modified    string         `json:"modified,omitempty"` // RFC 3339
	Links       []string       `json:"links,omitempty"`    // raw [[wikilink]] or @import targets
	Issues      []string       `json:"issues,omitempty"`   // machine codes, e.g. "legacy-frontmatter"
	Meta        map[string]any `json:"meta,omitempty"`     // kind-specific detail; never secret values
}
