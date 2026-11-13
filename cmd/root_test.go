package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/agent-report/internal/aggregate"
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

// marker is planted in every free-text field a transcript has. It must
// never reach the output, in either format. One kind of string does
// reach it by design — a version, a model id or a server name that has
// exactly that shape (AGENTS.md, "Closed sets, not redaction") — so the
// marker is planted in those fields in a form the patterns reject: a
// resource path, an address.
const marker = "ZQXSECRET"

// plantTranscripts writes a gem-agent transcript under a fake home whose
// every free-text field carries the marker, and returns the home.
func plantTranscripts(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "state", "gem-agent", "sessions", "projects", "-Users-"+marker)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		// Version and model values that a loose character-set pattern
		// would have let through.
		`{"ts":"2026-10-29T10:00:00Z","kind":"session","data":{"schema":2,"version":"v0.94.0-` + marker + `","model":"projects/` + marker + `-123/locations/us-central1/endpoints/9","project":"/Users/` + marker + `","location":"global"}}`,
		`{"ts":"2026-10-29T10:00:01Z","kind":"message","data":{"role":"user","content":"` + marker + ` please","attachments":[{"ref":"` + marker + `","kind":"hook","content":"` + marker + `"}]}}`,
		`{"ts":"2026-10-29T10:00:02Z","kind":"message","data":{"role":"assistant","content":"` + marker + `","tool_calls":[` +
			`{"id":"c1","name":"shell_exec","args":{"command":"cat /Users/` + marker + `/.env","access":"read","gem_agent_purpose":"` + marker + `"}},` +
			`{"id":"c5","name":"shell_exec","args":{"command":"x","access":"` + marker + `"}},` +
			`{"id":"c2","name":"mcp__` + marker + ` server__q","args":{"q":"` + marker + `"}},` +
			`{"id":"c3","name":"mcp__whois-lookup__lookup","args":{"query":"` + marker + `.example"}},` +
			`{"id":"c4","name":"` + marker + `_tool","args":{}}]}}`,
		`{"ts":"2026-10-29T10:00:05Z","kind":"message","data":{"role":"tool","tool_name":"shell_exec","tool_call_id":"c1","content":"` + marker + `"}}`,
		`{"ts":"2026-10-29T10:00:06Z","kind":"auto_decision","data":{"approved":true,"name":"shell_exec","reason":"` + marker + `","tier":"` + marker + `","key":"` + marker + `"}}`,
		`{"ts":"2026-10-29T10:00:07Z","kind":"gate_decision","data":{"decision":"denied","detail":"` + marker + `","deny_reason":"` + marker + `","source":"` + marker + `","name":"x"}}`,
		`{"ts":"2026-10-29T10:00:08Z","kind":"round_intervention","data":{"trigger":"` + marker + `","detail":"` + marker + `","reason":"` + marker + `"}}`,
		`{"ts":"2026-10-29T10:00:09Z","kind":"notice","data":{"message":"` + marker + `"}}`,
		// A model id is shown as written when it has a model id's shape (the
		// accepted residue, AGENTS.md); these two do not.
		`{"ts":"2026-10-29T10:00:10Z","kind":"usage","data":{"source":"` + marker + `","model":"projects/` + marker + `/locations/global/endpoints/9","prompt":10}}`,
		`{"ts":"2026-10-29T10:00:10.5Z","kind":"model_retry","data":{"model":"` + marker + `@example.com","source":"` + marker + `","attempt":2,"max":5,"cause":"` + marker + `","status":"` + marker + `","error":"` + marker + ` quota for project ` + marker + `","delay_ms":500}}`,
		`{"ts":"2026-10-29T10:00:11Z","kind":"` + marker + `","data":{"x":"` + marker + `"}}`,
		marker + ` not json`,
	}
	if err := os.WriteFile(filepath.Join(dir, marker+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ldir := filepath.Join(home, ".local", "state", "lagent", "sessions", "projects", "-Users-"+marker)
	if err := os.MkdirAll(ldir, 0o755); err != nil {
		t.Fatal(err)
	}
	llines := []string{
		`{"ts":"2026-10-29T11:00:00Z","kind":"session","data":{"schema":2,"version":"v0.19.2","model":"` + marker + `@example.com","project":"/Users/` + marker + `"}}`,
		`{"ts":"2026-10-29T11:00:01Z","kind":"message","data":{"role":"user","content":"Runtime facts for this session:\n- project: /Users/` + marker + `"}}`,
		`{"ts":"2026-10-29T11:00:02Z","kind":"message","data":{"role":"user","content":"` + marker + `"}}`,
		`{"ts":"2026-10-29T11:00:03Z","kind":"message","data":{"role":"assistant","content":"` + marker + `"}}`,
		`{"ts":"2026-10-29T11:00:04Z","kind":"tool_skipped","data":{"name":"` + marker + `","detail":"` + marker + `"}}`,
	}
	if err := os.WriteFile(filepath.Join(ldir, marker+".jsonl"), []byte(strings.Join(llines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestNoTranscriptTextReachesTheOutput(t *testing.T) {
	t.Setenv("HOME", plantTranscripts(t))
	for _, args := range [][]string{
		{"--since", "all"},
		{"--since", "all", "--json"},
		{"--since", "all", "--mcp-names"},
		{"--since", "all", "--mcp-names", "--json"},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr)
		}
		if strings.Contains(stdout, marker) || strings.Contains(stderr, marker) {
			t.Errorf("%v: transcript text leaked into the output:\n%s", args, stdout)
		}
	}
}

func TestReportCounts(t *testing.T) {
	t.Setenv("HOME", plantTranscripts(t))
	code, stdout, _ := runCLI("--since", "all", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep aggregate.Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Runtimes) != 2 || rep.Runtimes[0].Name != "gem-agent" || rep.Runtimes[1].Name != "lagent" {
		t.Fatalf("runtimes = %+v", rep.Runtimes)
	}
	g, l := rep.Runtimes[0], rep.Runtimes[1]
	if g.Transcripts != "found" || l.Transcripts != "found" {
		t.Errorf("transcripts = %s, %s; want found, found", g.Transcripts, l.Transcripts)
	}
	if g.Sessions.Count != 1 || g.Tools.MCP.Calls != 2 || g.Tools.UnknownNameCalls != 1 ||
		g.Shell.ByLane["read"] != 1 || g.Shell.ByLane["other"] != 1 ||
		g.Anomalies.UnknownRecordKinds != 1 || g.Anomalies.UnreadableLines != 1 {
		t.Errorf("gem-agent = %+v", g)
	}
	if l.Turns.Count != 1 || l.Models["other"] != 1 || l.Anomalies.UnknownRecordKinds != 0 {
		t.Errorf("lagent = %+v (the facts message is not a turn; tool_skipped is a known kind)", l)
	}
	if rep.Period.Since != "" || !strings.HasSuffix(rep.Period.Until, "Z") {
		t.Errorf("period = %+v; want no since and a UTC until", rep.Period)
	}
}

func TestMissingRuntimeAndMissingSessionsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, stdout, _ := runCLI("--runtime", "lagent", "--json")
	if code != 0 || !strings.Contains(stdout, `"transcripts": "missing"`) {
		t.Errorf("default location absent: exit %d\n%s", code, stdout)
	}
	code, _, stderr := runCLI("--runtime", "lagent", "--sessions-dir", "/no/such/dir")
	if code != 1 || !strings.Contains(stderr, "--sessions-dir") || strings.Contains(stderr, "/no/such/dir") {
		t.Errorf("named directory absent: exit %d, stderr %q; want 1 without echoing the path", code, stderr)
	}
}

func TestMCPNamesOnlyOnRequest(t *testing.T) {
	t.Setenv("HOME", plantTranscripts(t))
	_, without, _ := runCLI("--since", "all")
	_, with, _ := runCLI("--since", "all", "--mcp-names")
	if strings.Contains(without, "whois-lookup") {
		t.Error("an MCP server name appeared without --mcp-names")
	}
	if !strings.Contains(with, "whois-lookup: 1") {
		t.Errorf("--mcp-names did not list the server:\n%s", with)
	}
}

func TestSessionsDirOverride(t *testing.T) {
	home := plantTranscripts(t)
	t.Setenv("HOME", t.TempDir()) // the default location is now empty
	dir := filepath.Join(home, ".local", "state", "gem-agent", "sessions")
	code, stdout, _ := runCLI("--runtime", "gem-agent", "--sessions-dir", dir, "--since", "all", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep aggregate.Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Runtimes) != 1 || rep.Runtimes[0].Sessions.Count != 1 {
		t.Errorf("runtimes = %+v", rep.Runtimes)
	}
}

func TestDefaultPeriodExcludesOldSessions(t *testing.T) {
	t.Setenv("HOME", plantTranscripts(t))
	// fixedNow is 2026-10-30; the planted session is 2026-10-29.
	var out bytes.Buffer
	if code := run([]string{"--json"}, "v", fixedNow.AddDate(0, 2, 0), &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep aggregate.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Runtimes[0].Sessions.Count != 0 {
		t.Errorf("a session two months old was counted under the default 30 days")
	}
}
