// Package cmd implements the agent-report CLI.
//
// Flags use the standard library `flag` package (no third-party CLI
// framework): the tool has one command, and no dependency is the
// smallest supply-chain surface a third party can be asked to run.
package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nlink-jp/agent-report/internal/aggregate"
	"github.com/nlink-jp/agent-report/internal/render"
	"github.com/nlink-jp/agent-report/internal/transcript"
)

// Runtimes the tool reads, in report order.
var runtimes = []string{"gem-agent", "lagent"}

// Options is the validated command line.
type Options struct {
	Runtimes    []string  // one or both of runtimes
	Since       time.Time // zero = all time
	JSON        bool
	MCPNames    bool
	SessionsDir string // override; only with a single runtime
}

// Execute runs the CLI. version is injected from main via -ldflags.
func Execute(version string) {
	os.Exit(run(os.Args[1:], version, time.Now(), os.Stdout, os.Stderr))
}

// run is Execute without the process exit, so the CLI is testable.
// Exit codes: 0 success, 1 runtime error, 2 usage error.
func run(args []string, version string, now time.Time, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, versionLine(version))
		return 0
	}

	fs := flag.NewFlagSet("agent-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(fs.Output()) }
	runtime := fs.String("runtime", "all", "gem-agent, lagent, or all")
	since := fs.String("since", "30d", "period: <n>d, YYYY-MM-DD, or all")
	asJSON := fs.Bool("json", false, "emit JSON instead of the table")
	mcpNames := fs.Bool("mcp-names", false, "include call counts per MCP server name")
	sessionsDir := fs.String("sessions-dir", "", "read transcripts from this directory (single --runtime only)")
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, versionLine(version))
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument: %s\n\n", fs.Arg(0))
		usage(stderr)
		return 2
	}

	opts, err := buildOptions(*runtime, *since, *sessionsDir, now)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	opts.JSON = *asJSON
	opts.MCPNames = *mcpNames

	home, err := os.UserHomeDir()
	if err != nil && opts.SessionsDir == "" {
		fmt.Fprintf(stderr, "error: cannot find the home directory: %v\n", err)
		return 1
	}
	if err := report(opts, version, now, home, stdout); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// buildOptions validates the flag values that depend on each other.
func buildOptions(runtime, since, sessionsDir string, now time.Time) (Options, error) {
	var opts Options
	switch runtime {
	case "all":
		opts.Runtimes = append([]string(nil), runtimes...)
	case "gem-agent", "lagent":
		opts.Runtimes = []string{runtime}
	default:
		return Options{}, fmt.Errorf("--runtime must be gem-agent, lagent, or all (got %q)", runtime)
	}
	if sessionsDir != "" && len(opts.Runtimes) != 1 {
		return Options{}, errors.New("--sessions-dir needs a single --runtime (gem-agent or lagent)")
	}
	opts.SessionsDir = sessionsDir

	cutoff, err := parseSince(since, now)
	if err != nil {
		return Options{}, err
	}
	opts.Since = cutoff
	return opts, nil
}

// parseSince turns --since into the earliest time to count. "all" gives
// the zero time; "<n>d" counts back n whole days from now; a date is
// midnight at the start of that day in now's location.
func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "all" {
		return time.Time{}, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return time.Time{}, fmt.Errorf("--since %q: the day count must be a positive integer", s)
		}
		return now.AddDate(0, 0, -n), nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q: use <n>d, YYYY-MM-DD, or all", s)
	}
	if t.After(now) {
		return time.Time{}, fmt.Errorf("--since %q is in the future", s)
	}
	return t, nil
}

// sessionsDir is where a runtime keeps its transcripts.
func sessionsDir(home, runtime string) string {
	return filepath.Join(home, ".local", "state", runtime, "sessions")
}

// report reads the transcripts and writes the summary. Paths never reach
// the output: a missing default directory is reported as "missing", an
// unreadable file or directory as a count. Times are written in UTC so the
// report does not carry the reporter's time zone.
func report(opts Options, version string, now time.Time, home string, w io.Writer) error {
	rep := aggregate.Report{
		Schema:      aggregate.SchemaVersion,
		Tool:        "agent-report",
		ToolVersion: version,
		Period:      aggregate.Period{Until: now.UTC().Format(time.RFC3339)},
	}
	if !opts.Since.IsZero() {
		rep.Period.Since = opts.Since.UTC().Format(time.RFC3339)
	}
	for _, rt := range opts.Runtimes {
		dir := opts.SessionsDir
		if dir == "" {
			dir = sessionsDir(home, rt)
		}
		r, err := collect(rt, dir, opts)
		if err != nil {
			return err
		}
		rep.Runtimes = append(rep.Runtimes, r)
	}
	if opts.JSON {
		return render.JSON(w, rep)
	}
	return render.Table(w, rep)
}

// collect reads one runtime. A missing default directory means the
// runtime is not installed and is reported as such; a missing directory
// the operator named is a mistake and is an error.
func collect(runtime, dir string, opts Options) (aggregate.Runtime, error) {
	agg := aggregate.New(runtime, aggregate.Options{Since: opts.Since, MCPNames: opts.MCPNames})
	files, unreadableDirs, err := transcript.Discover(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if opts.SessionsDir != "" {
			return aggregate.Runtime{}, errors.New("--sessions-dir: no such directory")
		}
		agg.Missing()
		return agg.Result(), nil
	}
	if err != nil {
		agg.Unreadable()
	}
	agg.UnreadableDirs(unreadableDirs)
	for _, path := range files {
		f, err := transcript.ReadFile(path)
		if err != nil {
			agg.Unreadable()
			continue
		}
		agg.Add(f)
	}
	return agg.Result(), nil
}

// versionLine is what both `version` and `--version` print — identical, so
// a Homebrew formula test that runs `--version` and a person running
// `version` see the same string.
func versionLine(version string) string {
	return "agent-report " + version
}

func usage(w io.Writer) {
	fmt.Fprint(w, `agent-report — a usage summary of gem-agent and lagent sessions to attach to a report

Reads the session transcripts on this machine and prints fixed numeric items
only: no conversation, command, path or other free text leaves the transcripts.
Nothing is sent anywhere; you read the output and hand it over yourself.

Usage:
  agent-report [flags]
  agent-report version

Flags:
  --runtime gem-agent|lagent|all   runtime to report on (default all)
  --since <n>d|YYYY-MM-DD|all      period to count (default 30d)
  --json                           emit JSON instead of the table
  --mcp-names                      include call counts per MCP server name
  --sessions-dir <path>            read transcripts from this directory
                                   (single --runtime only)
  --version                        print the version and exit
`)
}
