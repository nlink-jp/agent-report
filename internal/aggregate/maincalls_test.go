package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/agent-report/internal/transcript"
)

const (
	mainModel = "gemini-4-carbon"
	sideModel = "gemini-4-hydrogen"
)

func usage(source, model string, prompt, cached int) map[string]any {
	return map[string]any{"source": source, "model": model, "prompt": prompt, "cached": cached, "output": 10, "thoughts": 5}
}

func retry(source, model, cause string, delayMS int) map[string]any {
	return map[string]any{"model": model, "source": source, "attempt": 2, "max": 5, "cause": cause, "status": "RESOURCE_EXHAUSTED", "delay_ms": delayMS}
}

func row(t *testing.T, cells []CallCell, bucket string) CallCell {
	t.Helper()
	for _, c := range cells {
		if c.Bucket == bucket {
			return c
		}
	}
	t.Fatalf("no row %q in %+v", bucket, cells)
	return CallCell{}
}

func total(cells []CallCell) int {
	n := 0
	for _, c := range cells {
		n += c.Calls
	}
	return n
}

// A call's wait runs from the message it answers to its usage record. The
// first call of a session is a reset. The later ones repeat the call
// before them and are sorted by how long ago that call ended (idle) and
// by how long ago it started (prefix age) — measured to this call's
// start, so neither holds this call's own length. The second call here
// takes 100 s: the third starts 3 s after it ended and 103 s after it
// started, and lands in different rows of the two tables.
func TestMainCallsWaitHitAndTables(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(6, "usage", usage("main", mainModel, 50_000, 0)).
		add(6, "message", calls(call("read_file", map[string]any{"path": "a"}))).
		add(7, "message", result("read_file", "x")).
		add(107, "usage", usage("main", mainModel, 250_000, 240_000)). // hit, waits 100 s
		add(107, "message", calls(call("read_file", map[string]any{"path": "b"}))).
		add(110, "message", result("read_file", "y")).
		add(130, "usage", usage("main", mainModel, 260_000, 0)). // miss, waits 20 s
		add(130, "message", answer())

	mc := run(Options{}, s.f).MainCalls
	if mc.Calls != 3 || mc.WaitSec.N != 3 || mc.WaitSecTotal != 126 || mc.WaitSec.Max != 100 {
		t.Fatalf("calls %d wait %+v total %v; want 3 calls, 6+100+20 s", mc.Calls, mc.WaitSec, mc.WaitSecTotal)
	}
	if c := row(t, mc.AfterReset, resetStart); c.Calls != 1 || c.Prompt != 50_000 || c.HitCalls != 0 || c.WaitSecMiss.Max != 6 {
		t.Errorf("session_start = %+v; want the first call, a 6 s miss", c)
	}
	if c := row(t, mc.ByIdle, "under_30s"); c.Calls != 2 || c.HitCalls != 1 || c.WaitSecHit.Max != 100 || c.WaitSecMiss.Max != 20 {
		t.Errorf("by_idle under_30s = %+v; want both later calls (1 s and 3 s after the call before ended)", c)
	}
	if total(mc.ByIdle) != 2 {
		t.Errorf("by_idle holds %d calls; want 2", total(mc.ByIdle))
	}
	if c := row(t, mc.ByPrefixAge, "under_30s"); c.Calls != 1 || c.HitCalls != 1 {
		t.Errorf("by_prefix_age under_30s = %+v; want the second call, 7 s after the first started", c)
	}
	if c := row(t, mc.ByPrefixAge, "60s_to_120s"); c.Calls != 1 || c.HitCalls != 0 || c.WaitSecMiss.Max != 20 {
		t.Errorf("by_prefix_age 60s_to_120s = %+v; want the third call, 103 s after the second started", c)
	}
	if c := row(t, mc.ByPromptSize, "under_100k"); c.Calls != 1 {
		t.Errorf("under_100k = %+v; want 1 call", c)
	}
	if c := row(t, mc.ByPromptSize, "200k_to_400k"); c.Calls != 2 || c.HitCalls != 1 || c.Prompt != 510_000 || c.Cached != 240_000 {
		t.Errorf("200k_to_400k = %+v; want 2 calls, 1 hit", c)
	}
}

// Every bucket is printed, in a fixed order, so two reports line up row
// for row and an empty row reads as zero rather than as missing.
func TestMainCallTablesPrintEveryBucketInOrder(t *testing.T) {
	mc := run(Options{}, newSession("v0.101.0", mainModel).f).MainCalls
	names := func(cells []CallCell) []string {
		var out []string
		for _, c := range cells {
			if c.Calls != 0 {
				t.Errorf("empty session has calls in %q", c.Bucket)
			}
			out = append(out, c.Bucket)
		}
		return out
	}
	gaps := []string{"under_30s", "30s_to_60s", "60s_to_120s", "120s_to_300s", "300s_to_900s", "900s_and_over"}
	want := map[string][]string{
		"size":  {"under_100k", "100k_to_200k", "200k_to_400k", "400k_to_600k", "600k_and_over"},
		"reset": {"session_start", "compaction", "resume", "clear", "tools_or_prompt_changed"},
		"idle":  gaps,
		"age":   gaps,
	}
	got := map[string][]string{"size": names(mc.ByPromptSize), "reset": names(mc.AfterReset), "idle": names(mc.ByIdle), "age": names(mc.ByPrefixAge)}
	for name, w := range want {
		g := got[name]
		if len(g) != len(w) {
			t.Fatalf("%s rows = %v; want %v", name, g, w)
		}
		for i := range g {
			if g[i] != w[i] {
				t.Errorf("%s rows = %v; want %v", name, g, w)
				break
			}
		}
	}
}

func TestBucketEdges(t *testing.T) {
	for prompt, want := range map[int]string{0: "under_100k", 99_999: "under_100k", 100_000: "100k_to_200k", 599_999: "400k_to_600k", 600_000: "600k_and_over"} {
		if got := sizeBucket(prompt); got != want {
			t.Errorf("sizeBucket(%d) = %q; want %q", prompt, got, want)
		}
	}
	for sec, want := range map[float64]string{0: "under_30s", 29.9: "under_30s", 30: "30s_to_60s", 899.9: "300s_to_900s", 900: "900s_and_over"} {
		if got := gapBucket(sec); got != want {
			t.Errorf("gapBucket(%v) = %q; want %q", sec, got, want)
		}
	}
}

// A hit is at least half the prompt cached: the provider caches a prefix,
// and a call that only matched the system prompt is not one.
func TestHitIsHalfThePromptOrMore(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(1, "usage", usage("main", mainModel, 100_000, 0)).
		add(2, "message", answer()).
		add(3, "message", user("a")).
		add(4, "usage", usage("main", mainModel, 100_000, 50_000)). // exactly half
		add(5, "message", answer()).
		add(6, "message", user("b")).
		add(7, "usage", usage("main", mainModel, 100_000, 49_999)).
		add(8, "message", answer())
	c := row(t, run(Options{}, s.f).MainCalls.ByIdle, "under_30s")
	if c.Calls != 2 || c.HitCalls != 1 {
		t.Errorf("under_30s = %+v; want 2 calls, 1 hit", c)
	}
}

// The first call after each kind of reset is sorted by the reset, not by
// time. A directory record resets only when it grants one mid-session;
// the same record written at start changes nothing.
func TestCallsAfterAReset(t *testing.T) {
	for _, c := range []struct {
		kind string
		data map[string]any
		want string // "" = no reset: the call is sorted by time
	}{
		{"compaction", map[string]any{"replaced": 4}, resetCompaction},
		{"resumed", map[string]any{"version": "v0.101.0"}, resetResume},
		{"clear", map[string]any{}, resetClear},
		{"mcp_advertise", map[string]any{}, resetDeclared},
		{"mcp_reload", map[string]any{}, resetDeclared},
		{"skills_reload", map[string]any{}, resetDeclared},
		{"added_directories", map[string]any{"dirs": []string{}, "granted": map[string]string{"access": "read"}}, resetDeclared},
		{"added_directories", map[string]any{"dirs": []string{}}, ""},
		{"notice", map[string]any{}, ""},
	} {
		s := newSession("v0.101.0", mainModel).
			add(0, "message", user("go")).
			add(1, "usage", usage("main", mainModel, 10_000, 0)).
			add(1, "message", answer()).
			add(2, c.kind, c.data).
			add(3, "message", user("again")).
			add(4, "usage", usage("main", mainModel, 10_000, 0)).
			add(4, "message", answer())
		mc := run(Options{}, s.f).MainCalls
		if c.want == "" {
			if total(mc.ByIdle) != 1 || total(mc.AfterReset) != 1 {
				t.Errorf("%s %v: after_reset %d, by_idle %d calls; want 1 and 1 (no reset)", c.kind, c.data, total(mc.AfterReset), total(mc.ByIdle))
			}
			continue
		}
		if got := row(t, mc.AfterReset, c.want).Calls; got != 1 || total(mc.ByIdle) != 0 {
			t.Errorf("%s: after_reset %s = %d calls, by_idle %d; want 1 and 0", c.kind, c.want, got, total(mc.ByIdle))
		}
	}
}

// A declaration change does not hide a stronger reason already waiting.
func TestTheStrongerResetNamesTheCall(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(1, "usage", usage("main", mainModel, 10_000, 0)).
		add(1, "message", answer()).
		add(2, "compaction", map[string]any{"replaced": 4}).
		add(3, "mcp_advertise", map[string]any{}).
		add(4, "message", user("a")).
		add(5, "usage", usage("main", mainModel, 10_000, 0)).
		add(5, "message", answer())
	mc := run(Options{}, s.f).MainCalls
	if row(t, mc.AfterReset, resetCompaction).Calls != 1 || row(t, mc.AfterReset, resetDeclared).Calls != 0 {
		t.Errorf("after_reset = %+v; want the call under compaction", mc.AfterReset)
	}
}

// What ends the work before a call moves the start of its wait, and each
// such record does so on its own. Every case gives the record a time no
// other start-mover shares, so removing one cannot be covered by another.
func TestWhatMovesTheStartOfAWait(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(*builder) *builder
		calls int
		wait  float64 // total
	}{
		{"a compaction: its record, not its usage record before it", func(b *builder) *builder {
			return b.add(30, "usage", usage("compact", mainModel, 5_000, 0)).
				add(40, "compaction", map[string]any{"replaced": 9}).
				add(45, "usage", usage("main", mainModel, 10_000, 0))
		}, 1, 5},
		{"another model call's usage record", func(b *builder) *builder {
			return b.add(20, "usage", usage("progress_review", sideModel, 1_000, 0)).
				add(23, "usage", usage("main", mainModel, 10_000, 0))
		}, 1, 3},
		{"the round-limit decision, written after the operator answered", func(b *builder) *builder {
			return b.add(20, "usage", usage("progress_review", sideModel, 1_000, 0)).
				add(50, "round_intervention", map[string]any{"trigger": "round-limit"}).
				add(54, "usage", usage("main", mainModel, 10_000, 0))
		}, 1, 4},
		{"the main call before, when an empty answer is sent again", func(b *builder) *builder {
			return b.add(10, "usage", usage("main", mainModel, 10_000, 0)).
				add(10, "assistant_empty", map[string]any{}).
				add(17, "usage", usage("main", mainModel, 10_000, 9_000))
		}, 2, 17},
		{"not a record that can land while the call is in flight", func(b *builder) *builder {
			return b.add(3, "notice", map[string]any{}).
				add(5, "mode_change", map[string]any{}).
				add(6, "model_retry", retry("main", mainModel, "429", 500)).
				add(9, "usage", usage("main", mainModel, 10_000, 0))
		}, 1, 9},
	} {
		s := c.build(newSession("v0.102.0", mainModel).add(0, "message", user("go"))).add(60, "message", answer())
		mc := run(Options{}, s.f).MainCalls
		if mc.Calls != c.calls || mc.WaitSecTotal != c.wait {
			t.Errorf("%s: calls %d, wait total %v; want %d, %v", c.name, mc.Calls, mc.WaitSecTotal, c.calls, c.wait)
		}
	}
}

// A main call with nothing before it has no start: it is counted, and
// has no wait.
func TestACallWithNoStartHasNoWait(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(5, "usage", usage("main", mainModel, 10_000, 0)).
		add(5, "message", answer())
	mc := run(Options{}, s.f).MainCalls
	if mc.Calls != 1 || mc.WaitSec.N != 0 || mc.WaitSecTotal != 0 {
		t.Errorf("calls %d wait %+v; want 1 call and no wait", mc.Calls, mc.WaitSec)
	}
	if c := row(t, mc.AfterReset, resetStart); c.Calls != 1 || c.WaitSecMiss.N != 0 {
		t.Errorf("session_start = %+v; want the call with no wait sample", c)
	}
}

// A retry is counted whoever made it. The part of a main call's wait
// spent before the attempt that answered began is the time to its last
// retry plus that retry's backoff — and only a retry the main loop made
// belongs to a main call.
func TestRetriesAndTheTimeTheyCost(t *testing.T) {
	s := newSession("v0.102.0", mainModel).
		add(0, "message", user("go")).
		add(20, "model_retry", retry("main", mainModel, "429", 500)).
		add(41, "model_retry", retry("main", mainModel, "429", 1000)). // the answered attempt begins at 42
		add(100, "usage", usage("main", mainModel, 300_000, 0)).
		add(100, "message", calls(call("shell_exec", map[string]any{"command": "ls"}))).
		add(101, "model_retry", retry("risk", sideModel, "503", 2000)).
		add(104, "usage", usage("risk", sideModel, 1_000, 0)).
		add(105, "message", result("shell_exec", "x")).
		add(106, "model_retry", retry("made_up_source", mainModel, "418", 500)).
		add(110, "usage", usage("main", mainModel, 301_000, 300_000)).
		add(110, "message", answer())
	rt := run(Options{}, s.f)
	r := rt.Retries
	if r.Count != 4 || r.SessionsRecording != 1 || r.BackoffSecTotal != 4 {
		t.Errorf("retries = %+v; want 4, 1 recording session, 4 s of backoff", r)
	}
	if !equal(r.ByCause, map[string]int{"429": 2, "503": 1, Other: 1}) {
		t.Errorf("by_cause = %v", r.ByCause)
	}
	if !equal(r.BySource, map[string]int{"main": 2, "risk": 1, Other: 1}) {
		t.Errorf("by_source = %v", r.BySource)
	}
	if !equal(r.ByModel, map[string]int{mainModel: 3, sideModel: 1}) {
		t.Errorf("by_model = %v", r.ByModel)
	}
	mc := rt.MainCalls
	if mc.CallsWithRetry != 1 || mc.RetrySecTotal != 42 || mc.WaitSecTotal != 105 {
		t.Errorf("calls_with_retry %d retry_sec_total %v wait_sec_total %v; want 1, 42, 100+5", mc.CallsWithRetry, mc.RetrySecTotal, mc.WaitSecTotal)
	}
}

// A retry is charged to a main call only when the main loop made it and
// that call is the one in flight. Each case would be charged by a rule
// that looked at the model's name or at what happened to come before.
func TestARetryBelongsOnlyToTheMainCallInFlight(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(*builder) *builder
	}{
		{"another kind of call on the main model that gave up and left no usage record", func(b *builder) *builder {
			return b.add(10, "model_retry", retry("compact", mainModel, "429", 500))
		}},
		{"a record that names no source", func(b *builder) *builder {
			return b.add(10, "model_retry", map[string]any{"model": mainModel, "cause": "429", "delay_ms": 500})
		}},
		{"a main call that gave up, before the operator's next message", func(b *builder) *builder {
			return b.add(10, "model_retry", retry("main", mainModel, "429", 500)).
				add(20, "message", user("again"))
		}},
		{"a main call's, once its own usage record is written", func(b *builder) *builder {
			return b.add(10, "model_retry", retry("main", mainModel, "429", 500)).
				add(15, "usage", usage("main", mainModel, 10_000, 0)).
				add(15, "assistant_empty", map[string]any{})
		}},
	} {
		s := c.build(newSession("v0.102.0", mainModel).add(0, "message", user("go"))).
			add(30, "usage", usage("main", mainModel, 10_000, 0)).
			add(30, "message", answer())
		rt := run(Options{Since: t0.Add(16 * time.Second)}, s.f) // only the last call is counted
		if mc := rt.MainCalls; mc.Calls != 1 || mc.CallsWithRetry != 0 || mc.RetrySecTotal != 0 {
			t.Errorf("%s: calls %d, calls_with_retry %d, retry_sec_total %v; want 1, 0, 0", c.name, mc.Calls, mc.CallsWithRetry, mc.RetrySecTotal)
		}
	}
}

// The time charged to retries never exceeds the call's own wait, and a
// negative delay is no delay.
func TestRetryTimeStaysInsideTheWait(t *testing.T) {
	s := newSession("v0.102.0", mainModel).
		add(0, "message", user("go")).
		add(5, "model_retry", retry("main", mainModel, "429", 100_000)). // would resume at 105
		add(10, "usage", usage("main", mainModel, 10_000, 0)).
		add(10, "message", answer()).
		add(20, "message", user("b")).
		add(22, "model_retry", retry("main", mainModel, "429", -5_000)).
		add(30, "usage", usage("main", mainModel, 10_000, 0)).
		add(30, "message", answer())
	rt := run(Options{}, s.f)
	if mc := rt.MainCalls; mc.CallsWithRetry != 2 || mc.RetrySecTotal != 12 {
		t.Errorf("calls_with_retry %d retry_sec_total %v; want 2 and 10+2 (the first clamped to its 10 s wait)", mc.CallsWithRetry, mc.RetrySecTotal)
	}
	if rt.Retries.BackoffSecTotal != 100 {
		t.Errorf("backoff_sec_total = %v; want 100 (the negative delay adds nothing)", rt.Retries.BackoffSecTotal)
	}
}

// A retry before the period is not counted, though the call it belongs
// to, answered inside the period, is still a call that was retried.
func TestRetryBeforeThePeriod(t *testing.T) {
	s := newSession("v0.102.0", mainModel).
		add(0, "message", user("go")).
		add(10, "model_retry", retry("main", mainModel, "429", 1000)).
		add(20, "usage", usage("main", mainModel, 10_000, 0)).
		add(20, "message", answer())
	rt := run(Options{Since: t0.Add(15 * time.Second)}, s.f)
	if rt.Retries.Count != 0 || rt.Retries.BackoffSecTotal != 0 {
		t.Errorf("retries = %+v; want none counted", rt.Retries)
	}
	if rt.MainCalls.CallsWithRetry != 1 || rt.MainCalls.RetrySecTotal != 11 {
		t.Errorf("calls_with_retry %d retry_sec_total %v; want 1 and 11", rt.MainCalls.CallsWithRetry, rt.MainCalls.RetrySecTotal)
	}
}

// Only a build that writes the record can show a retry: a zero count
// from an older build, or from lagent, is "not recorded". A session
// keeps the header of the build that started it, so one resumed by a
// newer build is told by the resume record.
func TestSessionsThatCouldRecordARetry(t *testing.T) {
	for _, c := range []struct {
		runtime, version string
		want             bool
	}{
		{"gem-agent", "v0.102.0", true},
		{"gem-agent", "v0.102.1-3-gabcdef0", true},
		{"gem-agent", "v1.0.0", true},
		{"gem-agent", "v0.101.0", false},
		{"gem-agent", "v0.101.0-5-g7e24c33-dirty", false}, // a build between tags cannot be placed
		{"gem-agent", "dev", false},
		{"lagent", "v0.102.0", false},
	} {
		if got := recordsRetries(c.runtime, c.version); got != c.want {
			t.Errorf("recordsRetries(%s, %s) = %v; want %v", c.runtime, c.version, got, c.want)
		}
	}
	a := New("gem-agent", Options{})
	a.Add(newSession("v0.101.0", mainModel).add(0, "message", user("x")).f)
	a.Add(newSession("v0.102.0", mainModel).add(0, "message", user("x")).f)
	a.Add(newSession("v0.99.0", mainModel).add(0, "message", user("x")).
		add(1, "resumed", map[string]any{"version": "v0.101.0"}).
		add(2, "resumed", map[string]any{"version": "v0.102.0"}).f)
	a.Add(newSession("v0.99.0", mainModel).add(0, "message", user("x")).
		add(1, "resumed", map[string]any{"version": "v0.101.0"}).f)
	a.Add(newSession("v0.101.0-5-g7e24c33-dirty", mainModel).add(0, "message", user("x")).
		add(1, "model_retry", retry("main", mainModel, "429", 500)).f)
	if got := a.Result().Retries.SessionsRecording; got != 3 {
		t.Errorf("sessions_recording = %d; want 3 of 5 (one by its header, one by a resume, one by holding a retry record)", got)
	}
	l := New("lagent", Options{})
	l.Add(newSession("v0.102.0", "gemma").add(0, "message", user("x")).
		add(1, "resumed", map[string]any{"version": "v0.102.0"}).f)
	if got := l.Result().Retries.SessionsRecording; got != 0 {
		t.Errorf("lagent sessions_recording = %d; want 0", got)
	}
}

// What a runtime does not record is said in its report: lagent writes no
// retry record and cannot tell "nothing cached" from "no count reported".
func TestNotRecordedIsSaidPerRuntime(t *testing.T) {
	for _, key := range []string{"retries", "cache_misses"} {
		if _, ok := NotRecorded("lagent")[key]; !ok {
			t.Errorf("lagent's report does not explain %q", key)
		}
		if _, ok := NotRecorded("gem-agent")[key]; ok {
			t.Errorf("gem-agent's report claims %q is not recorded", key)
		}
	}
	if _, ok := NotRecorded("gem-agent")["retries_in_older_sessions"]; !ok {
		t.Error("gem-agent's report does not say older sessions hold no retry record")
	}
}

// A call before the period is not counted, and the first call inside it
// is still compared with it: it is a repeat, not a session start.
func TestMainCallJustInsideThePeriod(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(5, "usage", usage("main", mainModel, 10_000, 0)).
		add(5, "message", answer()).
		add(100, "message", user("again")).
		add(140, "usage", usage("main", mainModel, 12_000, 10_000)).
		add(140, "message", answer())
	mc := run(Options{Since: t0.Add(50 * time.Second)}, s.f).MainCalls
	if mc.Calls != 1 || mc.WaitSecTotal != 40 {
		t.Fatalf("calls %d wait %v; want the one call inside the period, 40 s", mc.Calls, mc.WaitSecTotal)
	}
	if c := row(t, mc.AfterReset, resetStart); c.Calls != 0 {
		t.Errorf("session_start = %d calls; the session started before the period", c.Calls)
	}
	if c := row(t, mc.ByIdle, "60s_to_120s"); c.Calls != 1 || c.HitCalls != 1 {
		t.Errorf("by_idle 60s_to_120s = %+v; want the call, started 95 s after the one before ended", c)
	}
	if c := row(t, mc.ByPrefixAge, "60s_to_120s"); c.Calls != 1 {
		t.Errorf("by_prefix_age 60s_to_120s = %+v; want the call, started 100 s after the one before started", c)
	}
}

// Tokens are also summed by the model each usage record names, so a
// report can be priced without knowing which slot used which model. A
// usage record from before the source field came from the main loop.
func TestTokensByModelAndUnsourcedUsage(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(1, "usage", usage("main", mainModel, 1000, 400)).
		add(2, "usage", usage("risk", sideModel, 200, 0)).
		add(3, "usage", usage("main", "projects/p-1/locations/global/endpoints/9", 50, 0)).
		add(4, "usage", map[string]any{"prompt": 70, "output": 1}). // no source, no model
		add(5, "message", answer())
	rt := run(Options{}, s.f)
	want := map[string]TokenBucket{
		mainModel:  {Calls: 1, Prompt: 1000, Output: 10, Thoughts: 5, Cached: 400},
		sideModel:  {Calls: 1, Prompt: 200, Output: 10, Thoughts: 5},
		Other:      {Calls: 1, Prompt: 50, Output: 10, Thoughts: 5},
		Unrecorded: {Calls: 1, Prompt: 70, Output: 1},
	}
	if len(rt.TokensByModel) != len(want) {
		t.Fatalf("tokens_by_model = %+v", rt.TokensByModel)
	}
	for k, w := range want {
		if rt.TokensByModel[k] != w {
			t.Errorf("tokens_by_model[%s] = %+v; want %+v", k, rt.TokensByModel[k], w)
		}
	}
	if rt.MainCalls.Calls != 3 {
		t.Errorf("main calls = %d; want 3 (the unsourced record is the main loop's)", rt.MainCalls.Calls)
	}
}

// A retry or usage record that cannot be read is one unreadable line —
// not a crash, not a retry, and not counted twice.
func TestUnreadableRecordsAreCountedOnce(t *testing.T) {
	f := newSession("v0.102.0", mainModel).add(0, "message", user("go")).f
	f.Records = append(f.Records,
		transcript.Record{TS: t0.Add(time.Second), Kind: "model_retry", Data: []byte(`"oops"`)},
		transcript.Record{TS: t0.Add(2 * time.Second), Kind: "usage", Data: []byte(`"oops"`)})
	rt := run(Options{}, f)
	if rt.Retries.Count != 0 || rt.MainCalls.Calls != 0 || rt.Anomalies.UnreadableLines != 2 || rt.Anomalies.UnknownRecordKinds != 0 {
		t.Errorf("retries %d main calls %d unreadable %d unknown kinds %d; want 0, 0, 2, 0",
			rt.Retries.Count, rt.MainCalls.Calls, rt.Anomalies.UnreadableLines, rt.Anomalies.UnknownRecordKinds)
	}
}
