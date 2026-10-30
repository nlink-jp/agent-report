// Package aggregate turns transcripts into the report's numbers.
//
// Strings read from a transcript reach the report only as map keys that
// belong to a closed set: an enumerated value the runtimes write, a name
// on the known built-in tool list, or a name that matches a strict
// pattern. Everything else is counted under "other". Free text
// (conversation, commands, paths, reasons) is read only to recognise a
// fixed marker and is never copied out.
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
// the model, current and withdrawn. A call to any other non-MCP name (a
// model can invent one) counts as unknown.
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
	"assistant_empty",
)

var (
	tokenSources = setOf("main", "risk", "progress_review", "compact",
		"summarize_file", "web_search", "web_fetch", "agentic_file_search",
		"riskbook_learn", "librarian")
	tiers       = setOf("safe", "review", "block")
	gateSources = setOf("operator", "allowlist")
	triggers    = setOf("round-limit", "loop")
	shellLanes  = setOf("read", "write", "operator")
	versionRe   = regexp.MustCompile(`^(v?\d+\.\d+\.\d+([-+.][0-9A-Za-z.-]+)?|dev)$`)
	// A build from an untagged commit reports only its hash.
	untaggedRe = regexp.MustCompile(`^[0-9a-f]{7,40}(-dirty)?$`)
	modelRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,99}$`)
	serverRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// shellTimeoutMarker is the line both runtimes append to a shell result
// that ran out of time.
const shellTimeoutMarker = "[command timed out after"

// NotRecorded lists facts the transcripts do not hold, so a reader of the
// report does not take their absence for zero.
var NotRecorded = []string{"launch_mode"}

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
			NotRecorded:   append([]string(nil), NotRecorded...),
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

	s := sessionState{a: a}
	for _, r := range f.Records {
		if !a.inPeriod(r.TS) {
			continue
		}
		s.record(r)
	}
	s.finish()
}

// sessionState carries what spans records within one transcript.
type sessionState struct {
	a          *Aggregator
	inTurn     bool
	turnRounds int
	pending    []string // tool names awaiting results, in call order
	prevTS     time.Time
	maxPrompt  int
	sawUsage   bool
}

type toolCall struct {
	Name string `json:"name"`
	Args struct {
		Access *string `json:"access"`
	} `json:"args"`
}

type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolName  string     `json:"tool_name"`
	ToolCalls []toolCall `json:"tool_calls"`
}

func (s *sessionState) record(r transcript.Record) {
	a := s.a
	rt := &a.rt
	if !knownKinds[r.Kind] {
		rt.Anomalies.UnknownRecordKinds++
		return
	}
	switch r.Kind {
	case "message":
		var m message
		if json.Unmarshal(r.Data, &m) != nil {
			rt.Anomalies.UnreadableLines++
			return
		}
		s.message(r, m)
	case "usage":
		var u struct {
			Source   *string `json:"source"`
			Prompt   int     `json:"prompt"`
			Output   int     `json:"output"`
			Thoughts int     `json:"thoughts"`
			Cached   int     `json:"cached"`
		}
		if json.Unmarshal(r.Data, &u) != nil {
			rt.Anomalies.UnreadableLines++
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
			if u.Prompt > s.maxPrompt {
				s.maxPrompt = u.Prompt
			}
			a.promptMain += u.Prompt
			a.cachedMain += u.Cached
		}
	case "compaction":
		rt.Context.Compactions++
	case "clear":
		rt.Sessions.Cleared++
	case "resumed":
		rt.Sessions.Resumed++
	case "auto_decision":
		var d struct {
			Approved bool   `json:"approved"`
			Tier     string `json:"tier"`
			Model    bool   `json:"model"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			rt.Anomalies.UnreadableLines++
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
			rt.Anomalies.UnreadableLines++
			return
		}
		switch d.Decision {
		case "approved":
			rt.Approvals.Gate.Approved++
		case "denied":
			rt.Approvals.Gate.Denied++
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
		_ = json.Unmarshal(r.Data, &d)
		rt.Interventions[closedSet(d.Trigger, triggers)]++
	case "mode_start":
		var d struct {
			AutoApprove bool `json:"auto_approve"`
			ReadOnly    bool `json:"read_only"`
		}
		_ = json.Unmarshal(r.Data, &d)
		if d.AutoApprove {
			rt.Modes.SessionsStartedAutoApprove++
		}
		if d.ReadOnly {
			rt.Modes.SessionsStartedReadOnly++
		}
	case "mode_change":
		rt.Modes.ModeChanges++
	case "assistant_empty":
		rt.EmptyModelResponses++
	}
}

// runtimeNotePrefixes open user-role messages the runtime itself injects
// (a `!` shell escape, a resume note); they do not start an operator turn.
var runtimeNotePrefixes = []string{
	"I ran this shell command myself:",
	"gem-agent: this session was resumed on",
	"lagent: this session was resumed on",
}

func (s *sessionState) message(r transcript.Record, m message) {
	a := s.a
	rt := &a.rt
	switch m.Role {
	case "user":
		if hasAnyPrefix(m.Content, runtimeNotePrefixes) {
			return
		}
		s.endTurn()
		s.inTurn = true
	case "assistant":
		if s.inTurn {
			s.turnRounds++
		}
		if len(m.ToolCalls) == 0 {
			return
		}
		rt.Tools.ResponsesWithCalls++
		if len(m.ToolCalls) > 1 {
			rt.Tools.MultiCallResponses++
		}
		s.pending = s.pending[:0]
		s.prevTS = r.TS
		for _, c := range m.ToolCalls {
			s.pending = append(s.pending, c.Name)
			a.countCall(c)
		}
	case "tool":
		if len(s.pending) == 0 {
			rt.Anomalies.UnmatchedToolResults++
			return
		}
		name := s.pending[0]
		s.pending = s.pending[1:]
		sec := r.TS.Sub(s.prevTS).Seconds()
		if sec < 0 {
			sec = 0
		}
		s.prevTS = r.TS
		a.recordElapsed(name, sec)
		if name == "shell_exec" && strings.Contains(m.Content, shellTimeoutMarker) {
			rt.Shell.Timeouts++
		}
	}
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
		lane := Unrecorded
		if c.Args.Access != nil {
			switch v := *c.Args.Access; {
			case shellLanes[v]:
				lane = v
			case strings.HasPrefix(v, "unconfined"):
				lane = "unconfined"
			default:
				lane = Other
			}
		}
		rt.Shell.ByLane[lane]++
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
	if untaggedRe.MatchString(v) {
		return "dev"
	}
	return closed(v, versionRe)
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
