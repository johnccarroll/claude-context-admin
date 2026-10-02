package audit

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/johnccarroll/claude-context-admin/internal/scan"
)

// MemoryWriteProblems checks a file Claude just wrote. For a memory file it returns what Claude
// should fix (an unreadable header, or no line in MEMORY.md); for anything else, nothing.
func MemoryWriteProblems(path string) []string {
	dir := filepath.Dir(path)
	home, _ := os.UserHomeDir()
	// Only Claude Code's own memory folders (<config>/projects/<project>/memory), not any repo
	// folder that happens to be called memory.
	if filepath.Base(dir) != "memory" || filepath.Dir(filepath.Dir(dir)) != filepath.Join(scan.ConfigDir(home), "projects") ||
		!strings.HasSuffix(path, ".md") || filepath.Base(path) == "MEMORY.md" {
		return nil
	}
	src, err := scan.ReadText(path)
	if err != nil {
		return nil
	}
	var out []string
	if p := scan.HeaderProblem(src); p != "" {
		out = append(out, filepath.Base(path)+": "+p+".")
	}
	if idx, err := scan.ReadText(filepath.Join(dir, "MEMORY.md")); err == nil && !strings.Contains(string(idx), "("+filepath.Base(path)+")") {
		out = append(out, "MEMORY.md has no line for "+filepath.Base(path)+". Add one: - [Title]("+filepath.Base(path)+") — one-line hook.")
	}
	return out
}
