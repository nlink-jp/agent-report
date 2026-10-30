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

// ToolStat is one tool's calls. Elapsed runs from the end of the previous
// call in the same model response (from the response itself for the first
// call) to this call's result: the runtimes run a response's calls one
// after another, so it is the call's own time, including any time spent
// waiting for an approval or an answer.
type ToolStat struct {
	Calls      int  `json:"calls"`
	ElapsedSec Dist `json:"elapsed_sec"`
}

// Tools summarises tool calls.
type Tools struct {
	ResponsesWithCalls int                 `json:"responses_with_calls"`
	MultiCallResponses int                 `json:"multi_call_responses"`
	Builtin            map[string]ToolStat `json:"builtin"`
	UnknownNameCalls   int                 `json:"unknown_name_calls"`
	MCP                MCP                 `json:"mcp"`
}

// MCP summarises calls to MCP servers. ByServer is filled only when the
// operator asks for names (--mcp-names).
type MCP struct {
	Calls      int            `json:"calls"`
	Servers    int            `json:"servers"`
	Tools      int            `json:"tools"`
	ElapsedSec Dist           `json:"elapsed_sec"`
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

// GateApprovals are decisions taken at the approval prompt.
type GateApprovals struct {
	Approved      int            `json:"approved"`
	Denied        int            `json:"denied"`
	OtherDecision int            `json:"other_decision"`
	BySource      map[string]int `json:"by_source"`
	MustPrompt    int            `json:"must_prompt"`
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
}
