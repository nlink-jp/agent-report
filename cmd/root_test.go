package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errw bytes.Buffer
	code = run(args, "v0.0.0-test", fixedNow, &out, &errw)
	return code, out.String(), errw.String()
}

func TestVersionFlagAndSubcommandMatch(t *testing.T) {
	c1, o1, _ := runCLI("--version")
	c2, o2, _ := runCLI("version")
	if c1 != 0 || c2 != 0 {
		t.Fatalf("exit codes = %d, %d; want 0, 0", c1, c2)
	}
	if o1 != o2 {
		t.Fatalf("--version %q != version %q", o1, o2)
	}
	if want := "agent-report v0.0.0-test\n"; o1 != want {
		t.Fatalf("version line = %q; want %q", o1, want)
	}
}

func TestHelpExitsZero(t *testing.T) {
	code, _, stderr := runCLI("--help")
	if code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stderr, "Nothing is sent anywhere") {
		t.Fatalf("usage does not state that nothing is sent:\n%s", stderr)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{"--runtime", "claude"},
		{"--since", "0d"},
		{"--since", "yesterday"},
		{"--since", "2026-12-01"},
		{"--sessions-dir", "/tmp/x"},
		{"--no-such-flag"},
		{"extra"},
	}
	for _, args := range cases {
		if code, _, _ := runCLI(args...); code != 2 {
			t.Errorf("%v: exit code = %d; want 2", args, code)
		}
	}
}

func TestBuildOptions(t *testing.T) {
	opts, err := buildOptions("all", "30d", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(opts.Runtimes, ","); got != "gem-agent,lagent" {
		t.Errorf("runtimes = %s", got)
	}

	opts, err = buildOptions("lagent", "all", "/tmp/sessions", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Runtimes) != 1 || opts.Runtimes[0] != "lagent" || opts.SessionsDir != "/tmp/sessions" {
		t.Errorf("opts = %+v", opts)
	}
	if !opts.Since.IsZero() {
		t.Errorf("--since all gave %v; want the zero time", opts.Since)
	}
}

func TestParseSince(t *testing.T) {
	got, err := parseSince("30d", fixedNow)
	if err != nil || !got.Equal(fixedNow.AddDate(0, 0, -30)) {
		t.Errorf("30d = %v, %v", got, err)
	}
	got, err = parseSince("2026-09-01", fixedNow)
	if err != nil || !got.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("2026-09-01 = %v, %v", got, err)
	}
	if _, err := parseSince("-3d", fixedNow); err == nil {
		t.Error("-3d accepted")
	}
}

func TestReportNotImplementedIsRuntimeError(t *testing.T) {
	code, _, stderr := runCLI()
	if code != 1 || !strings.Contains(stderr, "not implemented") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
