// Command cca is Claude Context Admin.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/johnccarroll/claude-context-admin/internal/audit"
	"github.com/johnccarroll/claude-context-admin/internal/doctor"
	"github.com/johnccarroll/claude-context-admin/internal/loads"
	"github.com/johnccarroll/claude-context-admin/internal/mcpserver"
	"github.com/johnccarroll/claude-context-admin/internal/scan"
	"github.com/johnccarroll/claude-context-admin/internal/server"
	"github.com/johnccarroll/claude-context-admin/internal/usage"
	"github.com/johnccarroll/claude-context-admin/web"
)

// version is set at release time with -ldflags "-X main.version=…".
var version = "dev"

const usageText = `Claude Context Admin

Usage:
  cca                  Open the app (on macOS; elsewhere, or without it, in your browser)
  cca [serve] [flags]  Open it in your browser
  cca audit [flags]    Scan everything Claude Code loads and report problems
  cca doctor [--json]  Check that cca still understands this Claude Code version
  cca version          Print the version
  cca mcp              Run the MCP server (add it with: claude mcp add -s user cca -- cca mcp)

Serve flags:
  --port N          port on 127.0.0.1 (default 4317; 0 picks a free one)
  --read-only       never change any file
  --browser         use the browser even where the app is installed (any serve flag does)
  --no-open         print the link instead of opening the browser
  --home DIR        use a different home directory, e.g. a copy for safe testing

Audit flags:
  --json            print JSON
  --costs           include plugin token costs (runs ` + "`claude plugin details`" + ` per plugin)
  --project DIR     also show what loads at session start in DIR, and name collisions
  --no-usage        skip reading session transcripts
  --home DIR        home directory to scan (default: yours)
`

// appID is the Mac app's bundle identifier (macos/Info.plist).
const appID = "dev.johncarroll.claude-context-admin"

func main() {
	args := os.Args[1:]
	// A bare `cca` on macOS opens the app when it's installed (the app runs its own engine with
	// --no-open, so this never recurses). Without the app it falls through to the browser.
	if len(args) == 0 && runtime.GOOS == "darwin" {
		a := []string{"-b", appID}
		if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" { // apps don't inherit the shell's environment
			a = append(a, "--env", "CLAUDE_CONFIG_DIR="+d)
		}
		if exec.Command("/usr/bin/open", a...).Run() == nil {
			return
		}
	}
	if len(args) == 0 || len(args[0]) > 0 && args[0][0] == '-' {
		args = append([]string{"serve"}, args...)
	}
	switch args[0] {
	case "serve":
		os.Exit(runServe(args[1:]))
	case "audit":
		os.Exit(runAudit(args[1:]))
	case "doctor":
		os.Exit(runDoctor(args[1:]))
	case "version":
		fmt.Println("cca", version, "(Claude Context Admin)") // the plugin checks the name before running cca
		os.Exit(0)
	case "hook":
		os.Exit(runHook(args[1:]))
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ExitOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
		home := fs.String("home", "", "")
		_ = fs.Parse(args[1:])
		h, run := claudeFor(*home)
		if err := mcpserver.Run(context.Background(), h, version, run); err != nil {
			fmt.Fprintln(os.Stderr, "cca mcp:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}

// claudeFor resolves --home (empty means yours) and runs `claude` there, against that home.
func claudeFor(home string) (string, scan.Runner) {
	if home == "" {
		home, _ = os.UserHomeDir()
	} else if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r // /tmp/x is /private/tmp/x on macOS: compare paths the way they're recorded
	}
	return home, func(ctx context.Context, a ...string) ([]byte, error) { return server.ClaudeIn(ctx, home, home, a...) }
}

// opener opens a URL or file with the desktop's default app.
func opener(target string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.Command("/usr/bin/open", target)
	}
	return exec.Command("xdg-open", target)
}

// reveal shows a file in Finder on macOS, or opens its folder elsewhere.
func reveal(p string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("/usr/bin/open", "-R", p).Run()
	}
	return opener(filepath.Dir(p)).Run()
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	port := fs.Int("port", 4317, "")
	readOnly := fs.Bool("read-only", false, "")
	noOpen := fs.Bool("no-open", false, "")
	_ = fs.Bool("browser", false, "") // the browser is what serve always uses; the flag just skips the app
	home := fs.String("home", "", "")
	_ = fs.Parse(args)
	h, run := claudeFor(*home)
	static := web.Dist()
	devDir, devToken := os.Getenv("CCA_WEB_DIR"), ""
	if devDir != "" { // dev (scripts/dev.sh): serve the UI from disk; pages reload on rebuilds
		static = os.DirFS(devDir)
		if t := os.Getenv("CCA_DEV_TOKEN"); len(t) >= 32 { // only honoured in dev mode
			devToken = t
		}
	}
	s := &server.Server{Home: h, ReadOnly: *readOnly, Static: static, Run: run,
		Reveal: reveal, DevDir: devDir, FixedToken: devToken}
	ln, url, err := s.Listen(*port)
	if err != nil && *port != 0 { // usually another cca already running: take any free port
		fmt.Fprintf(os.Stderr, "cca: port %d is busy, using another one.\n", *port)
		ln, url, err = s.Listen(0)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cca: can't listen (%v).\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Claude Context Admin is running at\n  %s\n", url)
	if *readOnly {
		fmt.Println("Read-only: nothing will be changed.")
	}
	if !*noOpen {
		_ = opener(url).Start() // a headless box just keeps the printed URL
	}
	if err := s.Serve(ctx, ln); err != nil {
		fmt.Fprintln(os.Stderr, "cca:", err)
		return 1
	}
	return 0
}

// runHook handles Claude Code hook events (installed by the context-admin plugin). post-edit reads
// the PostToolUse event and, if Claude just wrote a memory file, reports problems back to Claude
// (exit 2 shows stderr to Claude). It never writes.
func runHook(args []string) int {
	if len(args) == 0 || args[0] != "post-edit" {
		return 0
	}
	var ev struct {
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if json.NewDecoder(os.Stdin).Decode(&ev) != nil || ev.ToolInput.FilePath == "" {
		return 0
	}
	if p := audit.MemoryWriteProblems(ev.ToolInput.FilePath); len(p) > 0 {
		fmt.Fprintln(os.Stderr, "Memory check (Context Admin): "+strings.Join(p, " "))
		return 2
	}
	return 0
}

// runDoctor exits 1 when a Claude Code format cca relies on has changed, so a scheduled run can alert.
func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "")
	home := fs.String("home", "", "")
	_ = fs.Parse(args)
	h, run := claudeFor(*home)
	checks := doctor.Run(context.Background(), h, run)
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": doctor.Worst(checks), "checks": checks})
	} else {
		mark := map[doctor.Status]string{doctor.OK: "ok  ", doctor.Warn: "warn", doctor.Fail: "FAIL"}
		for _, c := range checks {
			fmt.Printf("%s  %-18s %s\n", mark[c.Status], c.Name, c.Detail)
			if c.Fix != "" && c.Status != doctor.OK {
				fmt.Printf("      %s\n", c.Fix)
			}
		}
	}
	if doctor.Worst(checks) == doctor.Fail {
		return 1
	}
	return 0
}

type auditOutput struct {
	audit.Report
	Usage   usage.Usage    `json:"usage,omitempty"`
	Budget  *loads.Budget  `json:"budget,omitempty"`
	Shadows []loads.Shadow `json:"shadows,omitempty"`
}

func runAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	asJSON := fs.Bool("json", false, "")
	costs := fs.Bool("costs", false, "")
	project := fs.String("project", "", "")
	noUsage := fs.Bool("no-usage", false, "")
	home := fs.String("home", "", "")
	_ = fs.Parse(args)
	h, run := claudeFor(*home)
	ctx := context.Background()
	inv := (&scan.Scanner{Home: h, Run: run, Costs: *costs}).Scan(ctx)
	now := time.Now()
	var u usage.Usage
	if !*noUsage {
		u = usage.Scan(filepath.Join(scan.ConfigDir(h), "projects"), now.Add(-audit.StaleAfter), server.CachePath(h))
	}
	out := auditOutput{Report: audit.Run(inv, u, now), Usage: u}
	if *project != "" {
		p, _ := filepath.Abs(*project)
		b := loads.Compute(inv, p)
		out.Budget, out.Shadows = &b, loads.Shadows(inv, p)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	printText(out)
	return 0
}

func printText(out auditOutput) {
	for _, k := range slices.Sorted(maps.Keys(out.Counts)) {
		fmt.Printf("%-14s %d\n", k, out.Counts[k])
	}
	codes := map[string]int{}
	for _, f := range out.Findings {
		codes[f.Code]++
	}
	fmt.Printf("\n%d findings\n", len(out.Findings))
	for _, c := range slices.Sorted(maps.Keys(codes)) {
		fmt.Printf("  %-24s %d\n", c, codes[c])
	}
	if b := out.Budget; b != nil {
		fmt.Printf("\n~%d tokens load at session start in %s\n", b.Total, b.Project)
		for _, s := range b.Sources {
			fmt.Printf("  %-34s %7d  %s\n", s.Label, s.Tokens, s.Detail)
		}
		for _, s := range out.Shadows {
			fmt.Printf("  %s %q: %s wins over %d other(s)\n", s.Kind, s.Name, s.Winner.Scope, len(s.Hidden))
		}
	}
	for _, w := range out.Warnings {
		fmt.Println("warning:", w)
	}
}
