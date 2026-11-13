package aggregate

// The report's shape. Every field is a number, a boolean, or a map whose
// keys come from a closed set (enumerated values, a known tool list, or a
// name that passed a strict pattern). No string read from a transcript is
// copied out unless it is one of those keys.
//
// Field order is the order the table prints, so it reads top-down from
// "how much was used" to "what went wrong".

// SchemaVersion is the report format version.
const SchemaVersion = 1

// Report is the whole output.
type Report struct {
	Schema      int       `json:"schema"`
	Tool        string    `json:"tool"`
	ToolVersion string    `json:"tool_version"`
	Period      Period    `json:"period"`
	Runtimes    []Runtime `json:"runtimes"`
}

// Period is the window counted. Since is empty for all time.
type Period struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// Runtime is one runtime's summary.
type Runtime struct {
	Name                string                 `json:"name"`
	Transcripts         string                 `json:"transcripts"` // "found" or "missing"
	Sessions            Sessions               `json:"sessions"`
	Versions            map[string]int         `json:"versions"` // sessions per runtime version
	Models              map[string]int         `json:"models"`   // sessions per header model
	Turns               Turns                  `json:"turns"`
	Context             Context                `json:"context"`
	Tokens              map[string]TokenBucket `json:"tokens"` // per call source
	TokensByModel       map[string]TokenBucket `json:"tokens_by_model"`
	MainCalls           MainCalls              `json:"main_calls"`
	Retries             Retries                `json:"retries"`
	Tools               Tools                  `json:"tools"`
	Shell               Shell                  `json:"shell"`
	Approvals           Approvals              `json:"approvals"`
	Interventions       map[string]int         `json:"interventions"` // per trigger
	Modes               Modes                  `json:"modes"`
	EmptyModelResponses int                    `json:"empty_model_responses"`
	NotRecorded         map[string]string      `json:"not_recorded"` // item → why it is absent
	Anomalies           Anomalies              `json:"anomalies"`
}

// Sessions counts transcripts with activity in the period.
type Sessions struct {
	Count   int `json:"count"`
	Resumed int `json:"resumed"`
}

// Dist is a distribution summary.
type Dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	Max float64 `json:"max"`
}

// Turns describes operator turns: one user message and the model rounds
// that answered it.
type Turns struct {
	Count         int  `json:"count"`
	RoundsPerTurn Dist `json:"rounds_per_turn"`
}

// Context describes how large the conversation grew.
type Context struct {
	MaxPromptTokensPerSession Dist    `json:"max_prompt_tokens_per_session"`
	CachedShareOfPrompt       float64 `json:"cached_share_of_prompt"`
	Compactions               int     `json:"compactions"`
}

// TokenBucket is the spend of one call source.
type TokenBucket struct {
	Calls    int `json:"calls"`
	Prompt   int `json:"prompt"`
	Output   int `json:"output"`
	Thoughts int `json:"thoughts"`
	Cached   int `json:"cached"`
}

// MainCalls takes the main loop's model calls one at a time: how long each
// kept the operator waiting and how much of what it sent was served from
// the provider's cache. A call starts at the record that ended the work
// before it — the message it answers, a compaction, a round-limit
// decision, or another model call's usage record — and ends at its own
// usage record, so its wait is the model's time and holds any retry
// inside it. RetrySecTotal is the part of WaitSecTotal spent before the
// attempt that answered began: the refused attempts and the backoff
// after each, read from the runtime's retry records. Where no session
// could record a retry (see Retries) its zero, and CallsWithRetry's, mean
// "not recorded".
//
// The tables sort the same calls. A call is a hit when at least half of
// its prompt was cached. ByPromptSize takes every call. AfterReset takes
// the calls whose prompt could not have been sent before — the session's
// first, the first after a compaction, a resume or a clear, and the
// first after the tool declarations or the system prompt changed — where
// a miss is expected. The other calls repeat what the main call before
// them sent, and are sorted twice by how long ago that was when they
// started: ByIdle from the end of that call (the time between the two —
// tools, approvals, the operator), ByPrefixAge from its start (the same
// plus that call's own length). Neither includes the call's own length,
// so a slow call does not move itself down its table. A call whose
// predecessor's start is not known is in ByIdle only.
type MainCalls struct {
	Calls          int        `json:"calls"`
	WaitSec        Dist       `json:"wait_sec"`
	WaitSecTotal   float64    `json:"wait_sec_total"`
	CallsWithRetry int        `json:"calls_with_retry"`
	RetrySecTotal  float64    `json:"retry_sec_total"`
	ByPromptSize   []CallCell `json:"by_prompt_size"`
	AfterReset     []CallCell `json:"after_reset"`
	ByIdle         []CallCell `json:"by_idle"`
	ByPrefixAge    []CallCell `json:"by_prefix_age"`
}

// CallCell is one row of a MainCalls table. Bucket is one of a fixed
// list, and every bucket is printed, an empty one with zeros. Prompt and
// Cached are token sums; the waits are split by hit and miss because a
// miss on a large prompt is the slow case.
type CallCell struct {
	Bucket      string `json:"bucket"`
	Calls       int    `json:"calls"`
	Prompt      int    `json:"prompt"`
	Cached      int    `json:"cached"`
	HitCalls    int    `json:"hit_calls"`
	WaitSecHit  Dist   `json:"wait_sec_hit"`
	WaitSecMiss Dist   `json:"wait_sec_miss"`
}

// Retries counts the backoff retries of model calls, whichever call made
// them. Only a runtime build that writes a retry record can show any:
// SessionsRecording is how many of the counted sessions were started or
// resumed by such a build, and a zero Count beside a zero
// SessionsRecording means "not recorded", not "none". BackoffSecTotal
// adds up the waits between attempts; it leaves out the time the refused
// attempts themselves took. A call that gave up after its last attempt
// leaves its retries here and nothing in MainCalls, because the runtimes
// write no usage record for it.
type Retries struct {
	SessionsRecording int            `json:"sessions_recording"`
	Count             int            `json:"count"`
	ByCause           map[string]int `json:"by_cause"`
	BySource          map[string]int `json:"by_source"`
	ByModel           map[string]int `json:"by_model"`
	BackoffSecTotal   float64        `json:"backoff_sec_total"`
}

// ToolStat is one tool's calls. Elapsed runs from the end of the previous
// call in the same model response (from the response itself for the first
// call) to this call's result: the runtimes run a response's calls one
// after another, so it is the call's own time, including any time spent
// waiting for an approval or an answer. Run starts instead at the call's
// last approval decision, when it had one, so it leaves out the risk
// review and the wait at the prompt; a call that did not run (denied at
// the prompt or by a hook, or skipped) is not in it.
type ToolStat struct {
	Calls      int  `json:"calls"`
	ElapsedSec Dist `json:"elapsed_sec"`
	RunSec     Dist `json:"run_sec"`
}

// Tools summarises tool calls.
type Tools struct {
	ResponsesWithCalls int                  `json:"responses_with_calls"`
	MultiCallResponses int                  `json:"multi_call_responses"`
	MultiCallByKind    map[string]CallGroup `json:"multi_call_by_kind"`
	Builtin            map[string]ToolStat  `json:"builtin"`
	UnknownNameCalls   int                  `json:"unknown_name_calls"`
	MCP                MCP                  `json:"mcp"`
}

// CallGroup sums the responses with several calls of one kind: read_only
// (built-in tools the runtimes mark as not changing anything, and
// read-lane shell calls), with_mcp_or_web (those plus MCP, web_fetch or
// web_search calls), or other. Sequential is the run seconds of every
// call added up, as the runtimes ran them; if_parallel adds up only each
// response's longest call — the time had the calls run at once, an
// estimate that assumes they would not slow one another and could all
// start together. The runtimes answer every call, an interrupted one at
// once, so such a call counts with almost no time; only a response whose
// results are missing (the process died) is not counted.
type CallGroup struct {
	Responses     int     `json:"responses"`
	Calls         int     `json:"calls"`
	SequentialSec float64 `json:"sequential_sec"`
	IfParallelSec float64 `json:"if_parallel_sec"`
}

// MCP summarises calls to MCP servers. ByServer is filled only when the
// operator asks for names (--mcp-names).
type MCP struct {
	Calls      int            `json:"calls"`
	Servers    int            `json:"servers"`
	Tools      int            `json:"tools"`
	ElapsedSec Dist           `json:"elapsed_sec"`
	RunSec     Dist           `json:"run_sec"`
	ByServer   map[string]int `json:"by_server,omitempty"`
}

// Shell summarises shell_exec.
type Shell struct {
	ByLane   map[string]int `json:"by_lane"`
	Timeouts int            `json:"timeouts"`
}

// Approvals summarises the approval ladder.
type Approvals struct {
	Auto       AutoApprovals `json:"auto"`
	Gate       GateApprovals `json:"gate"`
	HookDenied int           `json:"hook_denied"`
}

// AutoApprovals are decisions the auto-approve ladder made.
type AutoApprovals struct {
	Approved       int            `json:"approved"`
	Escalated      int            `json:"escalated"`
	ByTier         map[string]int `json:"by_tier"`
	ModelConsulted int            `json:"model_consulted"`
}

// GateApprovals are decisions taken at the approval prompt. The operator
// wait runs from the call's start (or its auto-approve decision) to the
// operator's answer. A one-shot run denies at the prompt without asking
// and is recorded the same way, as a wait of almost nothing; how a run
// was launched is not recorded, so those cannot be told apart.
type GateApprovals struct {
	Approved        int            `json:"approved"`
	Denied          int            `json:"denied"`
	OtherDecision   int            `json:"other_decision"`
	BySource        map[string]int `json:"by_source"`
	MustPrompt      int            `json:"must_prompt"`
	OperatorWaitSec Dist           `json:"operator_wait_sec"`
}

// Modes counts session modes. A session counts once even when it was
// resumed and started again.
type Modes struct {
	SessionsAutoApproveAtStart int `json:"sessions_auto_approve_at_start"`
	SessionsReadOnlyAtStart    int `json:"sessions_read_only_at_start"`
	ModeChanges                int `json:"mode_changes"`
}

// Anomalies are what the reader could not use.
type Anomalies struct {
	UnreadableDirectories int `json:"unreadable_directories"`
	UnreadableFiles       int `json:"unreadable_files"`
	FilesWithoutHeader    int `json:"files_without_header"`
	UnsupportedSchema     int `json:"unsupported_schema"`
	UnreadableLines       int `json:"unreadable_lines"`
	UnknownRecordKinds    int `json:"unknown_record_kinds"`
	UnmatchedToolResults  int `json:"unmatched_tool_results"`
	// A decision record that names a call other than the one awaiting its
	// result: the run and wait times above assume the order the runtimes
	// write today.
	UnmatchedDecisions int `json:"unmatched_decisions"`
}
