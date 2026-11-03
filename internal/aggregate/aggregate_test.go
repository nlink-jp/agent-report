package aggregate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nlink-jp/agent-report/internal/transcript"
)

var t0 = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

// builder assembles a synthetic transcript; at is seconds after t0.
type builder struct{ f transcript.File }

func newSession(version, model string) *builder {
	return &builder{f: transcript.File{
		HasHeader: true, HeaderTS: t0,
		Header: transcript.Header{Schema: transcript.SupportedSchema, Version: version, Model: model},
	}}
}

func (b *builder) add(at float64, kind string, data any) *builder {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	b.f.Records = append(b.f.Records, transcript.Record{
		TS: t0.Add(time.Duration(at * float64(time.Second))), Kind: kind, Data: raw,
	})
	return b
}

func user(text string) map[string]any { return map[string]any{"role": "user", "content": text} }
func answer() map[string]any          { return map[string]any{"role": "assistant", "content": "done"} }
func result(name, content string) map[string]any {
	return map[string]any{"role": "tool", "tool_name": name, "content": content}
}
func calls(cs ...map[string]any) map[string]any {
	return map[string]any{"role": "assistant", "tool_calls": cs}
}
func call(name string, args map[string]any) map[string]any {
	return map[string]any{"name": name, "args": args}
}

func run(opts Options, files ...transcript.File) Runtime {
	a := New("gem-agent", opts)
	for _, f := range files {
		a.Add(f)
	}
	return a.Result()
}

func TestTurnsRoundsAndElapsed(t *testing.T) {
	s := newSession("v0.94.0", "gemini-3.8-flash").
		add(0, "message", user("first")).
		add(1, "message", calls(
			call("read_file", map[string]any{"path": "a"}),
			call("shell_exec", map[string]any{"command": "ls", "access": "read"}),
		)).
		add(3, "message", result("read_file", "x")).
		add(8, "message", result("shell_exec", "y")).
		add(9, "message", answer()).
		add(10, "message", user("I ran this shell command myself: ls")). // not a turn
		add(11, "message", user("second")).
		add(12, "message", answer())

	rt := run(Options{}, s.f)
	if rt.Sessions.Count != 1 || rt.Turns.Count != 2 {
		t.Fatalf("sessions %d turns %d; want 1, 2", rt.Sessions.Count, rt.Turns.Count)
	}
	if d := rt.Turns.RoundsPerTurn; d.N != 2 || d.Max != 2 || d.P50 != 1 {
		t.Errorf("rounds per turn = %+v; want n=2 p50=1 max=2", d)
	}
	if rt.Tools.ResponsesWithCalls != 1 || rt.Tools.MultiCallResponses != 1 {
		t.Errorf("responses %d multi %d", rt.Tools.ResponsesWithCalls, rt.Tools.MultiCallResponses)
	}
	if got := rt.Tools.Builtin["read_file"].ElapsedSec.Max; got != 2 {
		t.Errorf("read_file elapsed = %v; want 2 (call at 1s, result at 3s)", got)
	}
	if got := rt.Tools.Builtin["shell_exec"].ElapsedSec.Max; got != 5 {
		t.Errorf("shell_exec elapsed = %v; want 5 (previous result at 3s, this at 8s)", got)
	}
}

func TestShellLanesAndTimeouts(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(1, "message", calls(
			call("shell_exec", map[string]any{"command": "a", "access": "read"}),
			call("shell_exec", map[string]any{"command": "b", "access": "write"}),
			call("shell_exec", map[string]any{"command": "c", "access": "operator"}),
			call("shell_exec", map[string]any{"command": "d", "access": "unconfined:read"}),
			call("shell_exec", map[string]any{"command": "e"}),
			call("shell_exec", map[string]any{"command": "f", "access": "sideways"}),
		)).
		add(2, "message", result("shell_exec", "out\n[command timed out after 2m0s]"))

	rt := run(Options{}, s.f)
	want := map[string]int{"read": 1, "write": 1, "operator": 1, "unconfined": 1, Unrecorded: 1, Other: 1}
	for k, v := range want {
		if rt.Shell.ByLane[k] != v {
			t.Errorf("lane %s = %d; want %d (all %v)", k, rt.Shell.ByLane[k], v, rt.Shell.ByLane)
		}
	}
	if rt.Shell.Timeouts != 1 {
		t.Errorf("timeouts = %d; want 1", rt.Shell.Timeouts)
	}
}

func TestMCPAndUnknownNames(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(1, "message", calls(
			call("mcp__whois-lookup__lookup", nil),
			call("mcp__whois-lookup__get_usage", nil),
			call("mcp__splunk__search", nil),
			call("mcp__bad server!__x", nil),
			call("default_api:write_file", nil),
			call("github", nil),
		))

	rt := run(Options{}, s.f)
	if m := rt.Tools.MCP; m.Calls != 4 || m.Servers != 3 || m.Tools != 4 || m.ByServer != nil {
		t.Errorf("mcp = %+v; want 4 calls, 3 servers, 4 tools, no names", m)
	}
	if rt.Tools.UnknownNameCalls != 2 {
		t.Errorf("unknown = %d; want 2", rt.Tools.UnknownNameCalls)
	}

	rt = run(Options{MCPNames: true}, s.f)
	want := map[string]int{"whois-lookup": 2, "splunk": 1, Other: 1}
	for k, v := range want {
		if rt.Tools.MCP.ByServer[k] != v {
			t.Errorf("by_server %v; want %v", rt.Tools.MCP.ByServer, want)
		}
	}
}

func TestDecisionsInterventionsModes(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(1, "auto_decision", map[string]any{"approved": true, "tier": "safe", "model": false, "reason": "r"}).
		add(2, "auto_decision", map[string]any{"approved": false, "tier": "review", "model": true}).
		add(3, "auto_decision", map[string]any{"approved": false, "tier": "weird"}).
		add(4, "gate_decision", map[string]any{"decision": "approved", "source": "operator", "must_prompt": true}).
		add(5, "gate_decision", map[string]any{"decision": "denied", "source": "allowlist"}).
		add(6, "gate_decision", map[string]any{"decision": "approved", "source": "elsewhere"}).
		add(7, "hook_denied", map[string]any{"name": "x"}).
		add(8, "round_intervention", map[string]any{"trigger": "round-limit"}).
		add(9, "round_intervention", map[string]any{"trigger": "loop"}).
		add(10, "mode_start", map[string]any{"auto_approve": true, "read_only": true}).
		add(11, "mode_change", map[string]any{"setting": "read_only"}).
		add(12, "assistant_empty", map[string]any{}).
		add(13, "resumed", map[string]any{"version": "v1"}).
		add(14, "clear", map[string]any{}).
		add(15, "compaction", map[string]any{}).
		add(16, "brand_new_kind", map[string]any{}).
		add(17, "mode_start", map[string]any{"auto_approve": true}). // resumed: same session
		add(18, "riskbook_update", map[string]any{"action": "cleared"}).
		add(19, "tool_skipped", map[string]any{"name": "x"}).
		add(20, "gate_decision", map[string]any{"decision": "maybe", "source": "operator"})

	rt := run(Options{}, s.f)
	au := rt.Approvals.Auto
	if au.Approved != 1 || au.Escalated != 2 || au.ModelConsulted != 1 ||
		au.ByTier["safe"] != 1 || au.ByTier["review"] != 1 || au.ByTier[Other] != 1 {
		t.Errorf("auto = %+v", au)
	}
	g := rt.Approvals.Gate
	if g.Approved != 2 || g.Denied != 1 || g.OtherDecision != 1 || g.MustPrompt != 1 ||
		g.BySource["operator"] != 2 || g.BySource["allowlist"] != 1 || g.BySource[Other] != 1 {
		t.Errorf("gate = %+v", g)
	}
	if rt.Approvals.HookDenied != 1 || rt.Interventions["round-limit"] != 1 || rt.Interventions["loop"] != 1 {
		t.Errorf("hook %d interventions %v", rt.Approvals.HookDenied, rt.Interventions)
	}
	if rt.Modes != (Modes{SessionsAutoApproveAtStart: 1, SessionsReadOnlyAtStart: 1, ModeChanges: 1}) {
		t.Errorf("modes = %+v", rt.Modes)
	}
	if rt.EmptyModelResponses != 1 || rt.Sessions.Resumed != 1 || rt.Context.Compactions != 1 {
		t.Errorf("empty %d resumed %d compactions %d",
			rt.EmptyModelResponses, rt.Sessions.Resumed, rt.Context.Compactions)
	}
	if rt.Anomalies.UnknownRecordKinds != 1 {
		t.Errorf("unknown kinds = %d; want 1", rt.Anomalies.UnknownRecordKinds)
	}
}

func TestUsageSourcesAndContext(t *testing.T) {
	a := newSession("v0.94.0", "m").
		add(1, "usage", map[string]any{"source": "main", "prompt": 1000, "cached": 800, "output": 10}).
		add(2, "usage", map[string]any{"source": "main", "prompt": 3000, "cached": 1200}).
		add(3, "usage", map[string]any{"source": "risk", "prompt": 50000}).
		add(4, "usage", map[string]any{"source": "made_up", "prompt": 1})
	b := newSession("v0.94.0", "m").
		add(1, "usage", map[string]any{"prompt": 500, "cached": 0}) // pre-source record

	rt := run(Options{}, a.f, b.f)
	if rt.Tokens["main"].Calls != 2 || rt.Tokens["risk"].Calls != 1 || rt.Tokens[Other].Calls != 1 || rt.Tokens[Unrecorded].Calls != 1 {
		t.Errorf("tokens = %+v", rt.Tokens)
	}
	if d := rt.Context.MaxPromptTokensPerSession; d.N != 2 || d.Max != 3000 || d.P50 != 500 {
		t.Errorf("max prompt per session = %+v; want n=2 p50=500 max=3000 (risk calls excluded)", d)
	}
	if got := rt.Context.CachedShareOfPrompt; got != 0.444 {
		t.Errorf("cached share = %v; want 2000/4500 = 0.444", got)
	}
}

func TestSincePeriod(t *testing.T) {
	old := newSession("v0.94.0", "m").add(10, "message", user("old")).add(11, "message", answer())
	mixed := newSession("v0.94.0", "m").
		add(10, "message", user("before")).add(11, "message", answer()).
		add(7200, "message", user("after")).add(7201, "message", answer())

	rt := run(Options{Since: t0.Add(time.Hour)}, old.f, mixed.f)
	if rt.Sessions.Count != 1 {
		t.Errorf("sessions = %d; want 1 (only the one active in the period)", rt.Sessions.Count)
	}
	if rt.Turns.Count != 1 {
		t.Errorf("turns = %d; want 1 (records before since are not counted)", rt.Turns.Count)
	}
}

func TestClosedSetsForHeaderStrings(t *testing.T) {
	files := []transcript.File{
		newSession("v0.94.0", "gemini-3.8-flash").f,
		newSession("v0.68.1-11-g19eb157-dirty", "google/gemma-4-26b-a4b-qat").f,
		newSession("a403021-dirty", "/Users/someone/models/private.gguf").f,
		newSession("my secret build", "model with spaces").f,
		newSession("", "../../etc").f,
		// Strings that pass a loose pattern yet identify someone.
		newSession("v1.0.0-acmecorp.secret-project", "projects/my-gcp-proj-123456/locations/us-central1/endpoints/9").f,
		newSession("v1.0.0+acme", "user@example.com").f,
		newSession("dev", "home/me/models/client-x.gguf").f,
	}
	rt := run(Options{}, files...)
	wantV := map[string]int{"v0.94.0": 1, "v0.68.1-11-g19eb157-dirty": 1, "dev": 2, Other: 3, Unrecorded: 1}
	wantM := map[string]int{"gemini-3.8-flash": 1, "google/gemma-4-26b-a4b-qat": 1, Other: 6}
	if !equal(rt.Versions, wantV) {
		t.Errorf("versions = %v; want %v", rt.Versions, wantV)
	}
	if !equal(rt.Models, wantM) {
		t.Errorf("models = %v; want %v", rt.Models, wantM)
	}
}

func TestAnomalies(t *testing.T) {
	noHeader := transcript.File{BadLines: 2}
	oldSchema := newSession("v0.1.0", "m").f
	oldSchema.Header.Schema = 1
	orphan := newSession("v0.94.0", "m").add(1, "message", result("read_file", "x"))

	a := New("lagent", Options{})
	a.Add(noHeader)
	a.Add(oldSchema)
	a.Add(orphan.f)
	a.Unreadable()
	rt := a.Result()
	want := Anomalies{UnreadableFiles: 1, FilesWithoutHeader: 1, UnsupportedSchema: 1, UnreadableLines: 2, UnmatchedToolResults: 1}
	if rt.Anomalies != want {
		t.Errorf("anomalies = %+v; want %+v", rt.Anomalies, want)
	}
	if rt.Sessions.Count != 1 {
		t.Errorf("sessions = %d; want 1", rt.Sessions.Count)
	}
}

func TestMissingRuntime(t *testing.T) {
	a := New("lagent", Options{})
	a.Missing()
	rt := a.Result()
	if rt.Transcripts != "missing" || rt.Sessions.Count != 0 || len(rt.NotRecorded) == 0 {
		t.Errorf("rt = %+v", rt)
	}
}

func TestDist(t *testing.T) {
	d := dist([]float64{5, 1, 3, 2, 4, 10, 7, 8, 9, 6})
	if d != (Dist{N: 10, P50: 5, P90: 9, Max: 10}) {
		t.Errorf("dist = %+v", d)
	}
	if dist(nil) != (Dist{}) {
		t.Error("empty dist is not zero")
	}
}

func equal(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestRuntimeInjectedMessagesStartNoTurn(t *testing.T) {
	s := newSession("v0.19.2", "google/gemma-4-26b-a4b-qat").
		add(0, "message", user("Runtime facts for this session:\n- date: 2026-10-30")).
		add(1, "message", user("note from lagent: the shell_exec call abandoned by the interrupt completed")).
		add(2, "message", user("note from gem-agent: the read_file call abandoned by the interrupt completed")).
		add(3, "message", user("gem-agent: this session was resumed on 2026-10-30")).
		add(4, "message", user("hello")).
		add(5, "message", answer())
	rt := run(Options{}, s.f)
	if rt.Turns.Count != 1 || rt.Turns.RoundsPerTurn.Max != 1 {
		t.Errorf("turns %d rounds %+v; want 1 turn of 1 round", rt.Turns.Count, rt.Turns.RoundsPerTurn)
	}
}

func TestOddArgumentsKeepTheMessage(t *testing.T) {
	s := newSession("v0.19.2", "m").
		add(0, "message", user("go")).
		add(1, "message", calls(
			call("shell_exec", map[string]any{"command": "x", "access": 1}),
			call("read_file", map[string]any{"path": "a"}),
		)).
		add(2, "message", map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"name": "shell_exec", "args": "not an object"},
		}}).
		add(3, "message", result("shell_exec", "y"))
	rt := run(Options{}, s.f)
	if rt.Tools.Builtin["read_file"].Calls != 1 || rt.Tools.MultiCallResponses != 1 {
		t.Errorf("a non-string access lost its message: %+v", rt.Tools)
	}
	if rt.Shell.ByLane[Other] != 2 {
		t.Errorf("lanes = %v; want other=2", rt.Shell.ByLane)
	}
	if rt.Anomalies.UnreadableLines != 0 {
		t.Errorf("unreadable lines = %d; want 0", rt.Anomalies.UnreadableLines)
	}
}

func TestTimeoutOnlyOnTheLastLine(t *testing.T) {
	grep := "tools.go:1752: return result + \"\\n[command timed out after %s]\", nil\nmore output"
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(1, "message", calls(call("shell_exec", map[string]any{"access": "read"}))).
		add(2, "message", result("shell_exec", grep)).
		add(3, "message", calls(call("shell_exec", map[string]any{"access": "read"}))).
		add(4, "message", result("shell_exec", "partial\n[command timed out after 2m0s]\n"))
	if got := run(Options{}, s.f).Shell.Timeouts; got != 1 {
		t.Errorf("timeouts = %d; want 1 (the grep result only mentions the marker)", got)
	}
}

func TestCallBeforeThePeriodPairsWithItsResult(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(10, "message", calls(call("read_file", nil))).
		add(7200, "message", result("read_file", "late")).
		add(7201, "message", answer())
	rt := run(Options{Since: t0.Add(time.Hour)}, s.f)
	if rt.Anomalies.UnmatchedToolResults != 0 {
		t.Errorf("unmatched = %d; want 0 (the call was made before the period)", rt.Anomalies.UnmatchedToolResults)
	}
	if rt.Tools.Builtin["read_file"].Calls != 0 || rt.Turns.Count != 0 {
		t.Errorf("a call and turn from before the period were counted: %+v", rt)
	}
}

func TestUnreadableDecisionRecordsAreCounted(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(1, "round_intervention", "not an object").
		add(2, "mode_start", []int{1})
	rt := run(Options{}, s.f)
	if rt.Anomalies.UnreadableLines != 2 || len(rt.Interventions) != 0 || rt.Modes.SessionsAutoApproveAtStart != 0 {
		t.Errorf("anomalies %+v interventions %v modes %+v", rt.Anomalies, rt.Interventions, rt.Modes)
	}
}

func TestEveryKindTheRuntimesWriteIsKnown(t *testing.T) {
	// gem-agent writes connection_lost when the model connection drops
	// mid-round and library_log for a library's own log line; lagent
	// writes window_full when the context fills.
	s := newSession("v0.94.0", "m").
		add(1, "connection_lost", map[string]any{"round": 1}).
		add(2, "window_full", map[string]any{"round": 2}).
		add(3, "library_log", map[string]any{"line": "x"})
	if rt := run(Options{}, s.f); rt.Anomalies.UnknownRecordKinds != 0 {
		t.Errorf("unknown kinds = %d; want 0", rt.Anomalies.UnknownRecordKinds)
	}
}

func TestRunTimeLeavesOutApprovalWait(t *testing.T) {
	mcp := "mcp__srv__get"
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(10, "message", calls(call(mcp, nil), call(mcp, nil), call(mcp, nil), call("read_file", nil))).
		// first: risk review, then the operator answers after a long wait
		add(13, "auto_decision", map[string]any{"name": mcp, "approved": false, "tier": "review"}).
		add(70, "gate_decision", map[string]any{"name": mcp, "decision": "approved", "source": "operator"}).
		add(72, "message", result(mcp, "a")).
		// second: the allowlist answers at once — no operator wait
		add(73, "gate_decision", map[string]any{"name": mcp, "decision": "approved", "source": "allowlist"}).
		add(74, "message", result(mcp, "b")).
		// third: the operator denies it, so it never ran
		add(80, "gate_decision", map[string]any{"name": mcp, "decision": "denied", "source": "operator"}).
		add(80.5, "message", result(mcp, "denied")).
		// fourth: no decision at all, so run equals elapsed
		add(82.5, "message", result("read_file", "x")).
		add(83, "message", answer())

	rt := run(Options{}, s.f)
	m := rt.Tools.MCP
	if m.ElapsedSec.N != 3 || m.ElapsedSec.Max != 62 {
		t.Errorf("mcp elapsed = %+v; want n=3 max=62 (call at 10s, result at 72s)", m.ElapsedSec)
	}
	if m.RunSec.N != 2 || m.RunSec.Max != 2 || m.RunSec.P50 != 1 {
		t.Errorf("mcp run = %+v; want n=2 (the denied call left out) p50=1 max=2", m.RunSec)
	}
	w := rt.Approvals.Gate.OperatorWaitSec
	if w.N != 2 || w.Max != 57 || w.P50 != 6 {
		t.Errorf("operator wait = %+v; want n=2 p50=6 (74s→80s) max=57 (13s→70s)", w)
	}
	if r := rt.Tools.Builtin["read_file"]; r.RunSec != r.ElapsedSec || r.RunSec.Max != 2 {
		t.Errorf("read_file run %+v elapsed %+v; want both max=2", r.RunSec, r.ElapsedSec)
	}
	if rt.Anomalies.UnmatchedDecisions != 0 {
		t.Errorf("unmatched decisions = %d; want 0", rt.Anomalies.UnmatchedDecisions)
	}
}

func TestCallsStoppedBeforeRunningHaveNoRunTime(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(1, "message", calls(call("write_file", nil), call("edit_file", nil), call("shell_exec", nil))).
		add(2, "hook_denied", map[string]any{"name": "write_file", "reason": "r"}).
		add(2.1, "message", result("write_file", "denied")).
		add(3, "tool_skipped", map[string]any{"name": "edit_file"}).
		add(3.1, "message", result("edit_file", "skipped")).
		add(5, "message", result("shell_exec", "ok")).
		add(6, "message", answer())

	rt := run(Options{}, s.f)
	for _, name := range []string{"write_file", "edit_file"} {
		if st := rt.Tools.Builtin[name]; st.ElapsedSec.N != 1 || st.RunSec.N != 0 {
			t.Errorf("%s: elapsed %+v run %+v; want elapsed n=1, run n=0", name, st.ElapsedSec, st.RunSec)
		}
	}
	if st := rt.Tools.Builtin["shell_exec"]; st.RunSec.N != 1 || st.RunSec.Max != 1.9 {
		t.Errorf("shell_exec run = %+v; want n=1 max=1.9", st.RunSec)
	}
	if rt.Approvals.HookDenied != 1 {
		t.Errorf("hook denied = %d; want 1", rt.Approvals.HookDenied)
	}
	// write + edit did not run, shell ran 1.9s: the group sums only that.
	if g := rt.Tools.MultiCallByKind[Other]; g != (CallGroup{Responses: 1, Calls: 3, SequentialSec: 1.9, IfParallelSec: 1.9}) {
		t.Errorf("other group = %+v", g)
	}
}

func TestCallGroupsByKind(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		// read_only: a read tool and a read-lane shell call, 2s + 5s
		add(10, "message", calls(call("read_file", nil), call("shell_exec", map[string]any{"access": "read"}))).
		add(12, "message", result("read_file", "a")).
		add(17, "message", result("shell_exec", "b")).
		// with_mcp_or_web: a read tool, an MCP call and a web search, 1s + 4s + 3s
		add(20, "message", calls(call("list_files", nil), call("mcp__srv__get", nil), call("web_search", nil))).
		add(21, "message", result("list_files", "a")).
		add(25, "message", result("mcp__srv__get", "b")).
		add(28, "message", result("web_search", "c")).
		// other: a write-lane shell call makes the whole response other
		add(30, "message", calls(call("read_file", nil), call("shell_exec", map[string]any{"access": "write"}))).
		add(31, "message", result("read_file", "a")).
		add(33, "message", result("shell_exec", "b")).
		// a single call is not a group
		add(40, "message", calls(call("read_file", nil))).
		add(41, "message", result("read_file", "a")).
		// an interrupted response: one result of two arrives, then the turn ends
		add(50, "message", calls(call("read_file", nil), call("view_image", nil))).
		add(51, "message", result("read_file", "a")).
		add(60, "message", user("stop")).
		add(61, "message", calls(call("datetime", nil))).
		add(62, "message", result("datetime", "now")).
		add(63, "message", answer())

	rt := run(Options{}, s.f)
	want := map[string]CallGroup{
		"read_only":       {Responses: 1, Calls: 2, SequentialSec: 7, IfParallelSec: 5},
		"with_mcp_or_web": {Responses: 1, Calls: 3, SequentialSec: 8, IfParallelSec: 4},
		Other:             {Responses: 1, Calls: 2, SequentialSec: 3, IfParallelSec: 2},
	}
	if len(rt.Tools.MultiCallByKind) != len(want) {
		t.Errorf("groups = %+v; want %+v", rt.Tools.MultiCallByKind, want)
	}
	for k, w := range want {
		if got := rt.Tools.MultiCallByKind[k]; got != w {
			t.Errorf("%s = %+v; want %+v", k, got, w)
		}
	}
	if rt.Tools.MultiCallResponses != 4 {
		t.Errorf("multi-call responses = %d; want 4 (the interrupted one counts here)", rt.Tools.MultiCallResponses)
	}
}

func TestCallGroupOutsideThePeriodIsNotCounted(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(10, "message", calls(call("read_file", nil), call("list_files", nil))).
		add(7200, "message", result("read_file", "a")).
		add(7201, "message", result("list_files", "b"))
	rt := run(Options{Since: t0.Add(time.Hour)}, s.f)
	if len(rt.Tools.MultiCallByKind) != 0 {
		t.Errorf("groups = %+v; want none (the response was before the period)", rt.Tools.MultiCallByKind)
	}
}

func TestDecisionForAnotherCallIsAnAnomaly(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(1, "gate_decision", map[string]any{"name": "write_file", "decision": "approved", "source": "operator"}). // nothing pending
		add(2, "message", calls(call("write_file", nil), call("edit_file", nil))).
		add(3, "gate_decision", map[string]any{"name": "edit_file", "decision": "denied", "source": "operator"}). // not the first
		add(5, "message", result("write_file", "ok")).
		add(6, "message", result("edit_file", "ok"))
	rt := run(Options{}, s.f)
	if rt.Anomalies.UnmatchedDecisions != 2 {
		t.Errorf("unmatched decisions = %d; want 2", rt.Anomalies.UnmatchedDecisions)
	}
	// An unmatched decision changes no call: both ran, timed from the previous result.
	if w, e := rt.Tools.Builtin["write_file"].RunSec, rt.Tools.Builtin["edit_file"].RunSec; w.Max != 3 || e.Max != 1 {
		t.Errorf("write run %+v edit run %+v; want 3 and 1", w, e)
	}
	if rt.Approvals.Gate.OperatorWaitSec.N != 0 {
		t.Errorf("operator wait = %+v; want none (no decision matched a call)", rt.Approvals.Gate.OperatorWaitSec)
	}
}

func TestLaneDefaultRead(t *testing.T) {
	for _, c := range []struct {
		runtime, version string
		want             bool
	}{
		{"lagent", "v0.1.0", true},
		{"lagent", "dev", true},
		{"gem-agent", "v0.69.0", true},
		{"gem-agent", "v0.94.0-6-gee60cc3-dirty", true},
		{"gem-agent", "v1.0.0", true},
		{"gem-agent", "v0.68.9", false},
		{"gem-agent", "v0.9.0", false},
		{"gem-agent", "dev", false},
		{"gem-agent", "", false},
	} {
		if got := laneDefaultRead(c.runtime, c.version); got != c.want {
			t.Errorf("laneDefaultRead(%q, %q) = %v; want %v", c.runtime, c.version, got, c.want)
		}
	}
}

func TestShellWithNoLaneIsAReadWhereLanesExist(t *testing.T) {
	group := func(runtime, version string) map[string]CallGroup {
		s := newSession(version, "m").
			add(0, "message", user("go")).
			add(1, "message", calls(call("read_file", nil), call("shell_exec", map[string]any{"command": "ls"}))).
			add(2, "message", result("read_file", "a")).
			add(3, "message", result("shell_exec", "b"))
		a := New(runtime, Options{})
		a.Add(s.f)
		return a.Result().Tools.MultiCallByKind
	}
	for _, c := range []struct{ runtime, version, want string }{
		{"lagent", "v0.17.0", groupReadOnly},
		{"gem-agent", "v0.94.0", groupReadOnly},
		{"gem-agent", "v0.60.0", Other}, // before lanes every shell call could write
		{"gem-agent", "dev", Other},     // cannot be placed
	} {
		g := group(c.runtime, c.version)
		if len(g) != 1 || g[c.want].Responses != 1 {
			t.Errorf("%s %s: groups = %+v; want one %s", c.runtime, c.version, g, c.want)
		}
	}
}

func TestCeilingRefusalHasNoRunTime(t *testing.T) {
	s := newSession("v0.94.0", "m").
		add(0, "message", user("go")).
		add(1, "message", calls(call("write_file", nil))).
		add(40, "ceiling_refused", map[string]any{"name": "write_file"}).
		add(40.1, "message", result("write_file", "refused"))
	rt := run(Options{}, s.f)
	if st := rt.Tools.Builtin["write_file"]; st.ElapsedSec.N != 1 || st.RunSec.N != 0 {
		t.Errorf("elapsed %+v run %+v; want elapsed n=1, run n=0", st.ElapsedSec, st.RunSec)
	}
}
