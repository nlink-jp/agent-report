// Package aggregate turns transcripts into the report's numbers.
//
// Strings read from a transcript reach the report only as map keys that
// belong to a closed set: an enumerated value the runtimes write, a name
// on the known built-in tool list, or a version, model or MCP server name
// that matches a narrow pattern. Everything else is counted under
// "other". Free text (conversation, commands, paths, reasons) is read
// only to recognise a fixed marker and is never copied out.
package aggregate

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nlink-jp/agent-report/internal/transcript"
)

// Other is the key for a value outside a closed set.
const Other = "other"

// Unrecorded is the key for a value the record does not carry.
const Unrecorded = "unrecorded"

// BuiltinTools are the tool names gem-agent and lagent have declared to
// the model, current and withdrawn, as one list for both runtimes. A call
// to any other non-MCP name (a model can invent one) counts as unknown.
var BuiltinTools = setOf(
	"agent_info", "agentic_file_search", "ask_user", "datetime",
	"delete_memory", "edit_file", "file_info", "find_tools", "list_files",
	"list_tree", "load_skill", "mcp_load", "read_document", "read_file",
	"render_diagram", "request_directory", "save_memory", "search_files",
	"shell_exec", "show_image", "summarize_file", "view_image", "web_fetch",
	"web_search", "write_file",
)

// knownKinds are the record kinds the runtimes write. A kind outside it
// is counted as an anomaly: it is the first sign of a format this tool
// has not been taught.
var knownKinds = setOf(
	"message", "usage", "compaction", "clear", "resumed",
	"auto_decision", "gate_decision", "hook_denied", "round_intervention",
	"mode_start", "mode_change", "ceiling_auto", "ceiling_refused",
	"ceiling_lifted", "notice", "web_search", "web_fetch", "summary_usage",
	"agentic_search_usage", "librarian", "mcp_advertise", "mcp_reload",
	"mcp_fault", "skills_reload", "persistent_changes", "added_directories",
	"tool_late_return", "tool_excluded", "tool_not_advertised",
	"tool_skipped", "assistant_empty", "riskbook_update",
	"connection_lost", "window_full",
)

var (
	tokenSources = setOf("main", "risk", "progress_review", "compact",
		"summarize_file", "web_search", "web_fetch", "agentic_file_search",
		"riskbook_learn", "librarian")
	tiers       = setOf("safe", "review", "block")
	gateSources = setOf("operator", "allowlist")
	triggers    = setOf("round-limit", "loop")
	shellLanes  = setOf("read", "write", "operator")

	// A release tag, or `git describe` output for a build between tags:
	// digits and a commit hash only, so nothing else rides along.
	versionRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-\d+-g[0-9a-f]{7,40})?(-dirty)?$`)
	// A build from an untagged commit reports only its hash.
	untaggedRe = regexp.MustCompile(`^[0-9a-f]{7,40}(-dirty)?$`)
	// A model id, optionally under one publisher prefix ("google/gemma-…").
	// No "@" (an address), and at most one "/" (a resource path such as
	// projects/<id>/locations/… or a file path has more).
	modelRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,79}(/[A-Za-z0-9][A-Za-z0-9._:+-]{0,79})?$`)
	serverRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// shellTimeoutMarker opens the line both runtimes append as the last line
// of a shell result that ran out of time.
const shellTimeoutMarker = "[command timed out after"

// runtimeNotePrefixes open user-role messages the runtime itself injects;
// they do not start an operator turn.
var runtimeNotePrefixes = []string{
	"I ran this shell command myself:",          // `!` escape, both runtimes
	"gem-agent: this session was resumed on",    // gem-agent resume note
	"Runtime facts for this session:",           // lagent start, resume, /clear
	"note from gem-agent:", "note from lagent:", // late-return notes
}

// NotRecorded explains, inside the report itself, what the transcripts do
// not hold, so a reader does not take an absent item for zero.
func NotRecorded() map[string]string {
	return map[string]string{
		"launch_mode": "how each run was started (-p, --auto, --allow) is not written to the transcripts",
		"clear_count": "a /clear usually starts a new transcript without marking the old one, so it cannot be counted",
	}
}

// Options steer what is counted.
type Options struct {
	Since    time.Time // zero = all time
	MCPNames bool
}

// Aggregator accumulates one runtime.
type Aggregator struct {
	name string
	opts Options

	rt         Runtime
	rounds     []float64
	maxPrompt  []float64
	elapsed    map[string][]float64
	mcpElapsed []float64
	mcpServers map[string]bool
	mcpTools   map[string]bool
	promptMain int
	cachedMain int
}

// New starts an aggregator for the named runtime.
func New(name string, opts Options) *Aggregator {
	return &Aggregator{
		name: name,
		opts: opts,
		rt: Runtime{
			Name:          name,
			Transcripts:   "found",
			Versions:      map[string]int{},
			Models:        map[string]int{},
			Tokens:        map[string]TokenBucket{},
			Tools:         Tools{Builtin: map[string]ToolStat{}},
			Shell:         Shell{ByLane: map[string]int{}},
			Approvals:     Approvals{Auto: AutoApprovals{ByTier: map[string]int{}}, Gate: GateApprovals{BySource: map[string]int{}}},
			Interventions: map[string]int{},
			NotRecorded:   NotRecorded(),
		},
		elapsed:    map[string][]float64{},
		mcpServers: map[string]bool{},
		mcpTools:   map[string]bool{},
	}
}

// Missing marks the runtime's transcript directory as absent.
func (a *Aggregator) Missing() { a.rt.Transcripts = "missing" }

// Unreadable counts a transcript file that could not be opened or read.
func (a *Aggregator) Unreadable() { a.rt.Anomalies.UnreadableFiles++ }

// UnreadableDirs counts directories under the root that could not be read.
func (a *Aggregator) UnreadableDirs(n int) { a.rt.Anomalies.UnreadableDirectories += n }

func (a *Aggregator) inPeriod(t time.Time) bool {
	return a.opts.Since.IsZero() || !t.Before(a.opts.Since)
}

// Add counts one transcript.
func (a *Aggregator) Add(f transcript.File) {
	an := &a.rt.Anomalies
	an.UnreadableLines += f.BadLines
	if !f.HasHeader {
		an.FilesWithoutHeader++
		return
	}
	if f.Header.Schema != transcript.SupportedSchema {
		an.UnsupportedSchema++
		return
	}

	active := a.inPeriod(f.HeaderTS)
	for _, r := range f.Records {
		if a.inPeriod(r.TS) {
			active = true
			break
		}
	}
	if !active {
		return
	}
	a.rt.Sessions.Count++
	a.rt.Versions[version(f.Header.Version)]++
	a.rt.Models[closed(f.Header.Model, modelRe)]++

	// Records before the period are still walked, uncounted, so a call
	// made before the cutoff pairs with its result after it.
	s := sessionState{a: a}
	for _, r := range f.Records {
		s.record(r, a.inPeriod(r.TS))
	}
	s.finish()
}

// pendingCall is a tool call awaiting its result.
type pendingCall struct {
	name    string
	counted bool
}

// sessionState carries what spans records within one transcript.
type sessionState struct {
	a           *Aggregator
	inTurn      bool
	turnRounds  int
	pending     []pendingCall // in call order
	prevTS      time.Time
	maxPrompt   int
	sawUsage    bool
	autoAtStart bool
	roAtStart   bool
}

// The message fields are decoded leniently: model-written arguments are
// not schema-guaranteed, and one odd value must not lose the message.
type toolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type message struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []toolCall      `json:"tool_calls"`
}

// text returns a raw JSON string value, or "" for anything else.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func (s *sessionState) record(r transcript.Record, counted bool) {
	a := s.a
	rt := &a.rt
	if !counted {
		if r.Kind == "message" {
			var m message
			if json.Unmarshal(r.Data, &m) == nil {
				s.message(r, m, false)
			}
		}
		return
	}
	if !knownKinds[r.Kind] {
		rt.Anomalies.UnknownRecordKinds++
		return
	}
	unreadable := func() { rt.Anomalies.UnreadableLines++ }
	switch r.Kind {
	case "message":
		var m message
		if json.Unmarshal(r.Data, &m) != nil {
			unreadable()
			return
		}
		s.message(r, m, true)
	case "usage":
		var u struct {
			Source   *string `json:"source"`
			Prompt   int     `json:"prompt"`
			Output   int     `json:"output"`
			Thoughts int     `json:"thoughts"`
			Cached   int     `json:"cached"`
		}
		if json.Unmarshal(r.Data, &u) != nil {
			unreadable()
			return
		}
		src := Unrecorded
		if u.Source != nil {
			src = closedSet(*u.Source, tokenSources)
		}
		b := rt.Tokens[src]
		b.Calls++
		b.Prompt += u.Prompt
		b.Output += u.Output
		b.Thoughts += u.Thoughts
		b.Cached += u.Cached
		rt.Tokens[src] = b
		// Records written before the source field existed came from the
		// main loop only; they count toward context size too.
		if src == "main" || src == Unrecorded {
			s.sawUsage = true
			s.maxPrompt = max(s.maxPrompt, u.Prompt)
			a.promptMain += u.Prompt
			a.cachedMain += u.Cached
		}
	case "compaction":
		rt.Context.Compactions++
	case "resumed":
		rt.Sessions.Resumed++
	case "auto_decision":
		var d struct {
			Approved bool   `json:"approved"`
			Tier     string `json:"tier"`
			Model    bool   `json:"model"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			unreadable()
			return
		}
		if d.Approved {
			rt.Approvals.Auto.Approved++
		} else {
			rt.Approvals.Auto.Escalated++
		}
		rt.Approvals.Auto.ByTier[closedSet(d.Tier, tiers)]++
		if d.Model {
			rt.Approvals.Auto.ModelConsulted++
		}
	case "gate_decision":
		var d struct {
			Decision   string `json:"decision"`
			Source     string `json:"source"`
			MustPrompt bool   `json:"must_prompt"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			unreadable()
			return
		}
		switch d.Decision {
		case "approved":
			rt.Approvals.Gate.Approved++
		case "denied":
			rt.Approvals.Gate.Denied++
		default:
			rt.Approvals.Gate.OtherDecision++
		}
		rt.Approvals.Gate.BySource[closedSet(d.Source, gateSources)]++
		if d.MustPrompt {
			rt.Approvals.Gate.MustPrompt++
		}
	case "hook_denied":
		rt.Approvals.HookDenied++
	case "round_intervention":
		var d struct {
			Trigger string `json:"trigger"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			unreadable()
			return
		}
		rt.Interventions[closedSet(d.Trigger, triggers)]++
	case "mode_start":
		// Written once per process, so a resumed session has several;
		// the session counts once.
		var d struct {
			AutoApprove bool `json:"auto_approve"`
			ReadOnly    bool `json:"read_only"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			unreadable()
			return
		}
		s.autoAtStart = s.autoAtStart || d.AutoApprove
		s.roAtStart = s.roAtStart || d.ReadOnly
	case "mode_change":
		rt.Modes.ModeChanges++
	case "assistant_empty":
		rt.EmptyModelResponses++
	}
}

func (s *sessionState) message(r transcript.Record, m message, counted bool) {
	a := s.a
	rt := &a.rt
	switch m.Role {
	case "user":
		if hasAnyPrefix(text(m.Content), runtimeNotePrefixes) {
			return
		}
		s.endTurn()
		// A turn opened before the period is not counted, nor are the
		// rounds that answer it.
		s.inTurn = counted
	case "assistant":
		if s.inTurn && counted {
			s.turnRounds++
		}
		if len(m.ToolCalls) == 0 {
			return
		}
		if counted {
			rt.Tools.ResponsesWithCalls++
			if len(m.ToolCalls) > 1 {
				rt.Tools.MultiCallResponses++
			}
		}
		s.pending = s.pending[:0]
		s.prevTS = r.TS
		for _, c := range m.ToolCalls {
			s.pending = append(s.pending, pendingCall{name: c.Name, counted: counted})
			if counted {
				a.countCall(c)
			}
		}
	case "tool":
		if len(s.pending) == 0 {
			if counted {
				rt.Anomalies.UnmatchedToolResults++
			}
			return
		}
		call := s.pending[0]
		s.pending = s.pending[1:]
		// The runtimes run a response's calls one after another, so each
		// call's time runs from the previous result (from the response
		// itself for the first call).
		sec := max(r.TS.Sub(s.prevTS).Seconds(), 0)
		s.prevTS = r.TS
		if !call.counted || !counted {
			return
		}
		a.recordElapsed(call.name, sec)
		if call.name == "shell_exec" && timedOut(text(m.Content)) {
			rt.Shell.Timeouts++
		}
	}
}

// timedOut reports whether a shell result ends with the runtime's
// timeout line (and not merely mentions it, as a grep of the runtime's
// own source would).
func timedOut(content string) bool {
	content = strings.TrimRight(content, "\n")
	last := content[strings.LastIndexByte(content, '\n')+1:]
	return strings.HasPrefix(last, shellTimeoutMarker) && strings.HasSuffix(last, "]")
}

func (s *sessionState) endTurn() {
	if s.inTurn {
		s.a.rt.Turns.Count++
		s.a.rounds = append(s.a.rounds, float64(s.turnRounds))
	}
	s.inTurn, s.turnRounds = false, 0
}

func (s *sessionState) finish() {
	s.endTurn()
	if s.sawUsage {
		s.a.maxPrompt = append(s.a.maxPrompt, float64(s.maxPrompt))
	}
	if s.autoAtStart {
		s.a.rt.Modes.SessionsAutoApproveAtStart++
	}
	if s.roAtStart {
		s.a.rt.Modes.SessionsReadOnlyAtStart++
	}
}

func (a *Aggregator) countCall(c toolCall) {
	rt := &a.rt
	if server, ok := mcpServer(c.Name); ok {
		rt.Tools.MCP.Calls++
		a.mcpServers[server] = true
		a.mcpTools[c.Name] = true
		if a.opts.MCPNames {
			if rt.Tools.MCP.ByServer == nil {
				rt.Tools.MCP.ByServer = map[string]int{}
			}
			rt.Tools.MCP.ByServer[closed(server, serverRe)]++
		}
		return
	}
	if !BuiltinTools[c.Name] {
		rt.Tools.UnknownNameCalls++
		return
	}
	st := rt.Tools.Builtin[c.Name]
	st.Calls++
	rt.Tools.Builtin[c.Name] = st
	if c.Name == "shell_exec" {
		rt.Shell.ByLane[shellLane(c.Args)]++
	}
}

// shellLane reads the lane a shell call declared. A missing lane is
// unrecorded (older runtimes); a value that is not a known lane — or not
// a string at all — is other.
func shellLane(args json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil {
		return Other
	}
	raw, ok := fields["access"]
	if !ok {
		return Unrecorded
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return Other
	}
	switch {
	case shellLanes[v]:
		return v
	case strings.HasPrefix(v, "unconfined:"):
		return "unconfined"
	default:
		return Other
	}
}

func (a *Aggregator) recordElapsed(name string, sec float64) {
	if _, ok := mcpServer(name); ok {
		a.mcpElapsed = append(a.mcpElapsed, sec)
		return
	}
	if BuiltinTools[name] {
		a.elapsed[name] = append(a.elapsed[name], sec)
	}
}

// Result finishes the runtime's summary.
func (a *Aggregator) Result() Runtime {
	rt := a.rt
	rt.Turns.RoundsPerTurn = dist(a.rounds)
	rt.Context.MaxPromptTokensPerSession = dist(a.maxPrompt)
	if a.promptMain > 0 {
		rt.Context.CachedShareOfPrompt = round(float64(a.cachedMain)/float64(a.promptMain), 3)
	}
	builtin := make(map[string]ToolStat, len(rt.Tools.Builtin))
	for name, st := range rt.Tools.Builtin {
		st.ElapsedSec = dist(a.elapsed[name])
		builtin[name] = st
	}
	rt.Tools.Builtin = builtin
	rt.Tools.MCP.Servers = len(a.mcpServers)
	rt.Tools.MCP.Tools = len(a.mcpTools)
	rt.Tools.MCP.ElapsedSec = dist(a.mcpElapsed)
	return rt
}

// mcpServer returns the server part of an MCP tool name
// ("mcp__<server>__<tool>").
func mcpServer(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", false
	}
	server, _, ok := strings.Cut(rest, "__")
	if !ok || server == "" {
		return "", false
	}
	return server, true
}

func dist(v []float64) Dist {
	if len(v) == 0 {
		return Dist{}
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	rank := func(p float64) float64 {
		return s[max(int(math.Ceil(p*float64(len(s))))-1, 0)]
	}
	return Dist{N: len(s), P50: round(rank(0.5), 1), P90: round(rank(0.9), 1), Max: round(s[len(s)-1], 1)}
}

func round(x float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(x*p) / p
}

func version(v string) string {
	switch {
	case v == "":
		return Unrecorded
	case v == "dev" || untaggedRe.MatchString(v):
		return "dev"
	case versionRe.MatchString(v):
		return v
	default:
		return Other
	}
}

func closed(v string, re *regexp.Regexp) string {
	if v == "" {
		return Unrecorded
	}
	if re.MatchString(v) && !strings.Contains(v, "..") {
		return v
	}
	return Other
}

func closedSet(v string, set map[string]bool) string {
	if v == "" {
		return Unrecorded
	}
	if set[v] {
		return v
	}
	return Other
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
