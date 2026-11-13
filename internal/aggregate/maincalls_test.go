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

func retry(model, cause string, delayMS int) map[string]any {
	return map[string]any{"model": model, "attempt": 2, "max": 5, "cause": cause, "error": "Error 429", "delay_ms": delayMS}
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

// A call's wait runs from the message it answers to its usage record. The
// first call of a session is a reset (nothing could be cached); the next
// repeats it and is sorted by the gap since the first one ended.
func TestMainCallsWaitHitAndTables(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(6, "usage", usage("main", mainModel, 50_000, 0)).
		add(6, "message", calls(call("read_file", map[string]any{"path": "a"}))).
		add(7, "message", result("read_file", "x")).
		add(16, "usage", usage("main", mainModel, 250_000, 240_000)). // hit, 9 s, gap 10 s
		add(16, "message", calls(call("read_file", map[string]any{"path": "b"}))).
		add(17, "message", result("read_file", "y")).
		add(217, "usage", usage("main", mainModel, 260_000, 0)). // miss, 200 s, gap 201 s
		add(217, "message", answer())

	mc := run(Options{}, s.f).MainCalls
	if mc.Calls != 3 || mc.WaitSec.N != 3 || mc.WaitSecTotal != 215 || mc.WaitSec.Max != 200 {
		t.Fatalf("calls %d wait %+v total %v; want 3 calls, 6+9+200 s", mc.Calls, mc.WaitSec, mc.WaitSecTotal)
	}
	if c := row(t, mc.AfterReset, resetStart); c.Calls != 1 || c.Prompt != 50_000 || c.HitCalls != 0 || c.WaitSecMiss.Max != 6 {
		t.Errorf("session_start = %+v; want the first call, a 6 s miss", c)
	}
	if c := row(t, mc.ByGap, "under_30s"); c.Calls != 1 || c.HitCalls != 1 || c.Cached != 240_000 || c.WaitSecHit.Max != 9 || c.WaitSecMiss.N != 0 {
		t.Errorf("under_30s = %+v; want the second call, a 9 s hit", c)
	}
	if c := row(t, mc.ByGap, "120s_to_300s"); c.Calls != 1 || c.HitCalls != 0 || c.WaitSecMiss.Max != 200 || c.WaitSecHit.N != 0 {
		t.Errorf("120s_to_300s = %+v; want the third call, a 200 s miss", c)
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
	want := map[string][]string{
		"size":  {"under_100k", "100k_to_200k", "200k_to_400k", "400k_to_600k", "600k_and_over"},
		"reset": {"session_start", "compaction", "resume", "clear", "tools_or_prompt_changed"},
		"gap":   {"under_30s", "30s_to_60s", "60s_to_120s", "120s_to_300s", "300s_to_900s", "900s_and_over"},
	}
	for name, got := range map[string][]string{"size": names(mc.ByPromptSize), "reset": names(mc.AfterReset), "gap": names(mc.ByGap)} {
		if len(got) != len(want[name]) {
			t.Fatalf("%s rows = %v; want %v", name, got, want[name])
		}
		for i := range got {
			if got[i] != want[name][i] {
				t.Errorf("%s rows = %v; want %v", name, got, want[name])
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
	c := row(t, run(Options{}, s.f).MainCalls.ByGap, "under_30s")
	if c.Calls != 2 || c.HitCalls != 1 {
		t.Errorf("under_30s = %+v; want 2 calls, 1 hit", c)
	}
}

// The first call after each kind of reset is sorted by the reset, not by
// the gap; a declaration change does not hide a stronger reason waiting.
func TestCallsAfterAReset(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(1, "usage", usage("main", mainModel, 10_000, 0)).
		add(1, "message", answer()).
		add(2, "compaction", map[string]any{"replaced": 4}).
		add(2, "mcp_advertise", map[string]any{}). // compaction still names it
		add(3, "message", user("a")).
		add(4, "usage", usage("main", mainModel, 10_000, 0)).
		add(4, "message", answer()).
		add(5, "resumed", map[string]any{}).
		add(6, "message", user("b")).
		add(7, "usage", usage("main", mainModel, 10_000, 0)).
		add(7, "message", answer()).
		add(8, "mcp_advertise", map[string]any{}).
		add(9, "message", user("c")).
		add(10, "usage", usage("main", mainModel, 10_000, 0)).
		add(10, "message", answer()).
		add(11, "clear", map[string]any{}).
		add(12, "message", user("d")).
		add(13, "usage", usage("main", mainModel, 10_000, 0)).
		add(13, "message", answer()).
		add(14, "message", user("e")).
		add(15, "usage", usage("main", mainModel, 10_000, 9_000)).
		add(15, "message", answer())
	mc := run(Options{}, s.f).MainCalls
	for _, reason := range resetOrder {
		if c := row(t, mc.AfterReset, reason); c.Calls != 1 {
			t.Errorf("after_reset %s = %d calls; want 1", reason, c.Calls)
		}
	}
	gapCalls := 0
	for _, c := range mc.ByGap {
		gapCalls += c.Calls
	}
	if gapCalls != 1 {
		t.Errorf("by_gap holds %d calls; want only the last one", gapCalls)
	}
}

// Work that ends between the message and the call — a compaction, a side
// model call — moves the start of the wait: its time is not the main
// model's.
func TestWaitStartsWhereTheWorkBeforeEnded(t *testing.T) {
	s := newSession("v0.101.0", mainModel).
		add(0, "message", user("go")).
		add(40, "usage", usage("compact", mainModel, 5_000, 0)).
		add(40, "compaction", map[string]any{"replaced": 9}).
		add(45, "usage", usage("main", mainModel, 10_000, 0)). // 5 s, not 45
		add(45, "message", calls(call("shell_exec", map[string]any{"command": "ls"}))).
		add(48, "usage", usage("risk", sideModel, 1_000, 0)).
		add(49, "message", result("shell_exec", "x")).
		add(52, "usage", usage("main", mainModel, 11_000, 10_000)). // 3 s
		add(52, "message", answer())
	mc := run(Options{}, s.f).MainCalls
	if mc.Calls != 2 || mc.WaitSecTotal != 8 {
		t.Errorf("calls %d wait total %v; want 2 calls, 5+3 s", mc.Calls, mc.WaitSecTotal)
	}
	if c := row(t, mc.AfterReset, resetCompaction); c.Calls != 1 || c.WaitSecMiss.Max != 5 {
		t.Errorf("compaction row = %+v; want the 5 s call", c)
	}
}

// A retry is counted whichever model made it. The part of a main call's
// wait spent before the attempt that answered began is the time to its
// last retry plus that retry's backoff — and only a retry of the model
// the call was made to belongs to the call.
func TestRetriesAndTheTimeTheyCost(t *testing.T) {
	s := newSession("v0.102.0", mainModel).
		add(0, "message", user("go")).
		add(20, "model_retry", retry(mainModel, "429", 500)).
		add(41, "model_retry", retry(mainModel, "429", 1000)). // answered attempt begins at 42
		add(100, "usage", usage("main", mainModel, 300_000, 0)).
		add(100, "message", calls(call("shell_exec", map[string]any{"command": "ls"}))).
		add(101, "model_retry", retry(sideModel, "503", 2000)). // the risk call's
		add(104, "usage", usage("risk", sideModel, 1_000, 0)).
		add(105, "message", result("shell_exec", "x")).
		add(106, "model_retry", retry(sideModel, "error", 500)). // not this call's model
		add(110, "usage", usage("main", mainModel, 301_000, 300_000)).
		add(110, "message", answer())
	rt := run(Options{}, s.f)
	r := rt.Retries
	if r.Count != 4 || r.SessionsRecording != 1 || r.BackoffSecTotal != 4 {
		t.Errorf("retries = %+v; want 4, 1 recording session, 4 s of backoff", r)
	}
	if !equal(r.ByCause, map[string]int{"429": 2, "503": 1, "error": 1}) {
		t.Errorf("by_cause = %v", r.ByCause)
	}
	if !equal(r.ByModel, map[string]int{mainModel: 2, sideModel: 2}) {
		t.Errorf("by_model = %v", r.ByModel)
	}
	mc := rt.MainCalls
	if mc.CallsWithRetry != 1 || mc.RetrySecTotal != 42 || mc.WaitSecTotal != 105 {
		t.Errorf("calls_with_retry %d retry_sec_total %v wait_sec_total %v; want 1, 42, 100+5", mc.CallsWithRetry, mc.RetrySecTotal, mc.WaitSecTotal)
	}
}

// A retry of another call of the same model — a compaction, say — ended
// with that call's usage record and is not charged to the main call
// after it. A cause outside the runtime's list is counted as other.
func TestARetryBelongsToTheCallItPreceded(t *testing.T) {
	s := newSession("v0.102.0", mainModel).
		add(0, "message", user("go")).
		add(10, "model_retry", retry(mainModel, "418", 500)).
		add(30, "usage", usage("compact", mainModel, 5_000, 0)).
		add(30, "compaction", map[string]any{"replaced": 9}).
		add(34, "usage", usage("main", mainModel, 10_000, 0)).
		add(34, "message", answer())
	rt := run(Options{}, s.f)
	if rt.Retries.Count != 1 || rt.Retries.ByCause[Other] != 1 {
		t.Errorf("retries = %+v; want 1, cause other", rt.Retries)
	}
	if mc := rt.MainCalls; mc.CallsWithRetry != 0 || mc.RetrySecTotal != 0 || mc.WaitSecTotal != 4 {
		t.Errorf("calls_with_retry %d retry_sec_total %v wait %v; want 0, 0, 4", mc.CallsWithRetry, mc.RetrySecTotal, mc.WaitSecTotal)
	}
}

// Only a build that writes the record can show a retry: a zero count
// from an older build, or from lagent, is "not recorded".
func TestSessionsThatCouldRecordARetry(t *testing.T) {
	for _, c := range []struct {
		runtime, version string
		want             bool
	}{
		{"gem-agent", "v0.102.0", true},
		{"gem-agent", "v0.102.1-3-gabcdef0", true},
		{"gem-agent", "v1.0.0", true},
		{"gem-agent", "v0.101.0", false},
		{"gem-agent", "v0.101.0-3-g3cbc915-dirty", false}, // cannot be placed after the tag
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
	if got := a.Result().Retries.SessionsRecording; got != 1 {
		t.Errorf("sessions_recording = %d; want 1 of 2", got)
	}
	if _, ok := NotRecorded("lagent")["retries"]; !ok {
		t.Error("lagent's report does not say retries are not recorded")
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
	if c := row(t, mc.ByGap, "120s_to_300s"); c.Calls != 1 || c.HitCalls != 1 {
		t.Errorf("120s_to_300s = %+v; want the call, 135 s after the one before", c)
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

// A retry record that cannot be read is an unreadable line, not a crash
// and not a retry.
func TestUnreadableRetryRecord(t *testing.T) {
	f := newSession("v0.102.0", mainModel).add(0, "message", user("go")).f
	f.Records = append(f.Records, transcript.Record{TS: t0.Add(time.Second), Kind: "model_retry", Data: []byte(`"oops"`)})
	rt := run(Options{}, f)
	if rt.Retries.Count != 0 || rt.Anomalies.UnreadableLines != 1 || rt.Anomalies.UnknownRecordKinds != 0 {
		t.Errorf("retries %d unreadable %d unknown kinds %d; want 0, 1, 0", rt.Retries.Count, rt.Anomalies.UnreadableLines, rt.Anomalies.UnknownRecordKinds)
	}
}
