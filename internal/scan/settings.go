package scan

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
)

// Instruction-file modes (settings key `instructionFiles`, Claude Code 2.1.277+).
const (
	ModeClaudeOnly     = "claude-md"
	ModeAgentsFallback = "claude-md-or-agents-md" // default: AGENTS.md when the project has no CLAUDE.md
	ModeBoth           = "claude-md-and-agents-md"
	ModeManagedOnly    = "managed-only"
)

// legacyModes maps the older `projectInstructions` values, which Claude Code still honours.
var legacyModes = map[string]string{"none": ModeManagedOnly, "claude": ModeClaudeOnly,
	"agents-fallback": ModeAgentsFallback, "both": ModeBoth}

// ManagedSettings is the organization's settings file (set by IT on managed machines). It wins
// over every other layer. A variable so tests can point it elsewhere.
var ManagedSettings = map[string]string{"darwin": "/Library/Application Support/ClaudeCode/managed-settings.json",
	"linux": "/etc/claude-code/managed-settings.json"}[runtime.GOOS]

// InstructionMode resolves which instruction files Claude Code loads in project, reading user,
// project, local and managed settings (later layers win). Unknown values fall back to the default.
func InstructionMode(home, project string) string {
	mode := ModeAgentsFallback
	layers := []string{filepath.Join(ConfigDir(home), "settings.json")}
	if project != "" {
		layers = append(layers, filepath.Join(project, ".claude", "settings.json"), filepath.Join(project, ".claude", "settings.local.json"))
	}
	if ManagedSettings != "" {
		layers = append(layers, ManagedSettings)
	}
	for _, p := range layers {
		b, err := ReadText(p)
		if err != nil {
			continue
		}
		var s struct {
			InstructionFiles    string `json:"instructionFiles"`
			ProjectInstructions string `json:"projectInstructions"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		switch {
		case slices.Contains([]string{ModeClaudeOnly, ModeAgentsFallback, ModeBoth, ModeManagedOnly}, s.InstructionFiles):
			mode = s.InstructionFiles
		case legacyModes[s.ProjectInstructions] != "":
			mode = legacyModes[s.ProjectInstructions]
		}
	}
	return mode
}
