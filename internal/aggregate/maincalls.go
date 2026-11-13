package aggregate

import (
	"encoding/json"
	"time"

	"github.com/nlink-jp/agent-report/internal/transcript"
)

// The main loop's model calls, one at a time (MainCalls), and the retry
// records (Retries). Both are read from what the runtimes write: a usage
// record after each call, the records that end the work before it, and —
// from the builds that have it — a model_retry record for each backoff.
// A retry belongs to the main call in flight when its source is "main";
// nothing else of the record is used to place it.

// sizeBuckets and gapBuckets are the fixed rows of the MainCalls tables,
// in print order; the last row of each takes everything above.
var (
	sizeBuckets = []struct {
		under int
		name  string
	}{
		{100_000, "under_100k"}, {200_000, "100k_to_200k"},
		{400_000, "200k_to_400k"}, {600_000, "400k_to_600k"},
	}
	gapBuckets = []struct {
		under float64
		name  string
	}{
		{30, "under_30s"}, {60, "30s_to_60s"}, {120, "60s_to_120s"},
		{300, "120s_to_300s"}, {900, "300s_to_900s"},
	}
)

const (
	sizeTop = "600k_and_over"
	gapTop  = "900s_and_over"
)

// Why a main call's prompt could not have been sent before.
const (
	resetStart      = "session_start"
	resetCompaction = "compaction"
	resetResume     = "resume"
	resetClear      = "clear"
	// The tool declarations or the system prompt changed: both come
	// before the conversation in the request, so the whole of it is new.
	resetDeclared = "tools_or_prompt_changed"
)

var resetOrder = []string{resetStart, resetCompaction, resetResume, resetClear, resetDeclared}

// retryCauses are the values the runtime writes as a retry's cause: the
// transient statuses it retries on.
var retryCauses = setOf("429", "500", "502", "503", "504")

// retryEra is the first gem-agent release that records a retry
// (gem-agent ADR-0116).
var retryEra = [3]int{0, 102, 0}

// recordsRetries reports whether a session of this runtime and version
// could hold a retry record. lagent writes none.
func recordsRetries(runtime, v string) bool {
	return runtime == "gem-agent" && releaseAtLeast(v, retryEra)
}

func sizeBucket(prompt int) string {
	for _, b := range sizeBuckets {
		if prompt < b.under {
			return b.name
		}
	}
	return sizeTop
}

func gapBucket(sec float64) string {
	for _, b := range gapBuckets {
		if sec < b.under {
			return b.name
		}
	}
	return gapTop
}

// cellSum accumulates a CallCell before its distributions are drawn.
type cellSum struct {
	calls, prompt, cached, hits int
	hitWait, missWait           []float64
}

func (c *cellSum) add(prompt, cached int, hit, timed bool, wait float64) {
	c.calls++
	c.prompt += prompt
	c.cached += cached
	if hit {
		c.hits++
	}
	if !timed {
		return
	}
	if hit {
		c.hitWait = append(c.hitWait, wait)
	} else {
		c.missWait = append(c.missWait, wait)
	}
}

// callSums accumulates MainCalls and the retry backoff for one runtime.
type callSums struct {
	calls, withRetry             int
	wait                         []float64
	waitTotal, retryTotal        float64
	backoffSec                   float64
	bySize, reset, byIdle, byAge map[string]*cellSum
}

func newCallSums() callSums {
	return callSums{
		bySize: map[string]*cellSum{}, reset: map[string]*cellSum{},
		byIdle: map[string]*cellSum{}, byAge: map[string]*cellSum{},
	}
}

func cell(m map[string]*cellSum, key string) *cellSum {
	c := m[key]
	if c == nil {
		c = &cellSum{}
		m[key] = c
	}
	return c
}

// table draws every bucket in order, an empty one with zeros.
func table(m map[string]*cellSum, order []string) []CallCell {
	out := make([]CallCell, 0, len(order))
	for _, name := range order {
		c := m[name]
		if c == nil {
			c = &cellSum{}
		}
		out = append(out, CallCell{
			Bucket: name, Calls: c.calls, Prompt: c.prompt, Cached: c.cached, HitCalls: c.hits,
			WaitSecHit: dist(c.hitWait), WaitSecMiss: dist(c.missWait),
		})
	}
	return out
}

func (c *callSums) result() MainCalls {
	sizes := make([]string, 0, len(sizeBuckets)+1)
	for _, b := range sizeBuckets {
		sizes = append(sizes, b.name)
	}
	sizes = append(sizes, sizeTop)
	gaps := make([]string, 0, len(gapBuckets)+1)
	for _, b := range gapBuckets {
		gaps = append(gaps, b.name)
	}
	gaps = append(gaps, gapTop)
	return MainCalls{
		Calls:          c.calls,
		WaitSec:        dist(c.wait),
		WaitSecTotal:   round(c.waitTotal, 1),
		CallsWithRetry: c.withRetry,
		RetrySecTotal:  round(c.retryTotal, 1),
		ByPromptSize:   table(c.bySize, sizes),
		AfterReset:     table(c.reset, resetOrder),
		ByIdle:         table(c.byIdle, gaps),
		ByPrefixAge:    table(c.byAge, gaps),
	}
}

// mainCallState is what one transcript carries from record to record for
// the per-call tables.
type mainCallState struct {
	// callStart is when the work before the next model call ended: the
	// last message, compaction, round-limit decision or usage record.
	callStart time.Time
	// prevMainStart and prevMainEnd bound the main call before; the
	// start is zero when that call's own start was not known.
	prevMainStart, prevMainEnd time.Time
	// reset names why the next main call's prompt is new; "" when it
	// repeats what the call before sent.
	reset string
	// retried holds, for each retry of the main call now in flight, when
	// the attempt after it began.
	retried []time.Time
	// recording: this session was started or resumed by a build that
	// writes a retry record.
	recording bool
}

// moveStart marks t as the end of the work before the next model call.
// Whatever was in flight before it is over, so its retries are too: a
// main call that gave up after its last attempt wrote no usage record,
// and the next call must not inherit its retries.
func (s *sessionState) moveStart(t time.Time) {
	s.callStart, s.retried = t, s.retried[:0]
}

// track follows every record, in or out of the period, and counts the
// main calls and retries that are in it.
func (s *sessionState) track(r transcript.Record, counted bool) {
	switch r.Kind {
	case "message":
		s.moveStart(r.TS)
	case "compaction":
		s.reset = resetCompaction
		s.moveStart(r.TS)
	case "round_intervention":
		// Written after the review and after the operator answered the
		// round-limit dialog: the model call starts here, not at the
		// message, or the dialog's wait would count as the model's.
		s.moveStart(r.TS)
	case "resumed":
		s.reset = resetResume
		var d struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(r.Data, &d) == nil && recordsRetries(s.a.name, d.Version) {
			s.recording = true
		}
	case "clear":
		s.reset = resetClear
	case "mcp_advertise", "mcp_reload", "skills_reload":
		s.declared()
	case "added_directories":
		// The record is also written at start and after /clear, where it
		// changes nothing; a directory granted mid-session rebuilds the
		// system prompt, and only that one carries "granted".
		var d map[string]json.RawMessage
		if json.Unmarshal(r.Data, &d) == nil {
			if _, ok := d["granted"]; ok {
				s.declared()
			}
		}
	case "model_retry":
		var d struct {
			Model   string  `json:"model"`
			Source  *string `json:"source"`
			Cause   string  `json:"cause"`
			DelayMS int     `json:"delay_ms"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			if counted {
				s.a.rt.Anomalies.UnreadableLines++
			}
			return
		}
		// A session that holds a retry record could record one, whatever
		// its version says: a build between two tags cannot be placed.
		s.recording = true
		delay := time.Duration(max(d.DelayMS, 0)) * time.Millisecond
		src := Unrecorded
		if d.Source != nil {
			src = closedSet(*d.Source, tokenSources)
		}
		if src == "main" {
			s.retried = append(s.retried, r.TS.Add(delay))
		}
		if counted {
			rt := &s.a.rt
			rt.Retries.Count++
			rt.Retries.ByCause[closedSet(d.Cause, retryCauses)]++
			rt.Retries.BySource[src]++
			rt.Retries.ByModel[closed(d.Model, modelRe)]++
			s.a.calls.backoffSec += delay.Seconds()
		}
	case "usage":
		var u struct {
			Source *string `json:"source"`
			Prompt int     `json:"prompt"`
			Cached int     `json:"cached"`
		}
		if json.Unmarshal(r.Data, &u) != nil {
			return // the usage case of record counts it as unreadable
		}
		// A record from before the source field came from the main loop.
		if u.Source != nil && *u.Source != "main" {
			s.moveStart(r.TS) // another model call ended here
			return
		}
		if counted {
			s.mainCall(r.TS, u.Prompt, u.Cached)
		}
		s.prevMainStart, s.prevMainEnd, s.reset = s.callStart, r.TS, ""
		s.moveStart(r.TS)
	}
}

// declared notes that the tool declarations or the system prompt
// changed. A stronger reason already waiting says more than this one.
func (s *sessionState) declared() {
	if s.reset == "" {
		s.reset = resetDeclared
	}
}

// mainCall counts one main-loop call whose usage record was written at ts.
func (s *sessionState) mainCall(ts time.Time, prompt, cached int) {
	c := &s.a.calls
	c.calls++
	timed := !s.callStart.IsZero()
	wait := 0.0
	if timed {
		wait = max(ts.Sub(s.callStart).Seconds(), 0)
		c.wait = append(c.wait, wait)
		c.waitTotal += wait
	}
	// The attempt that answered began when the last retry's backoff ran
	// out; everything before that was refused attempts and waiting.
	var resumed time.Time
	for _, t := range s.retried {
		if t.After(resumed) {
			resumed = t
		}
	}
	if !resumed.IsZero() {
		c.withRetry++
		if timed {
			c.retryTotal += min(max(resumed.Sub(s.callStart).Seconds(), 0), wait)
		}
	}

	hit := prompt > 0 && cached*2 >= prompt
	cell(c.bySize, sizeBucket(prompt)).add(prompt, cached, hit, timed, wait)
	switch {
	case s.reset != "":
		cell(c.reset, s.reset).add(prompt, cached, hit, timed, wait)
	case timed && !s.prevMainEnd.IsZero():
		idle := max(s.callStart.Sub(s.prevMainEnd).Seconds(), 0)
		cell(c.byIdle, gapBucket(idle)).add(prompt, cached, hit, timed, wait)
		if !s.prevMainStart.IsZero() {
			age := max(s.callStart.Sub(s.prevMainStart).Seconds(), 0)
			cell(c.byAge, gapBucket(age)).add(prompt, cached, hit, timed, wait)
		}
	}
}
