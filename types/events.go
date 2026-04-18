package types

import "encoding/json"

// ThreadEvent is the interface implemented by every streamed event yielded
// by thread.RunStreamed() and codex.Query(). The interface is sealed via
// the unexported isThreadEvent() marker.
//
// Server-initiated approval requests do NOT flow through this channel —
// they go to the ApprovalCallback configured on CodexOptions. Only events
// that don't require a client response appear here.
type ThreadEvent interface {
	isThreadEvent()
	// EventMethod returns the wire-level JSON-RPC method that produced
	// this event (e.g., "turn/started"). UnknownEvent returns its own
	// Method field.
	EventMethod() string
}

// ThreadStarted is emitted once when thread/start completes successfully.
// It carries the server-assigned thread ID.
type ThreadStarted struct {
	ThreadID string `json:"thread_id"`
}

func (*ThreadStarted) isThreadEvent()      {}
func (*ThreadStarted) EventMethod() string { return "thread/started" }

// TurnStarted is emitted after turn/start is accepted and the agent begins
// working.
type TurnStarted struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
}

func (*TurnStarted) isThreadEvent()      {}
func (*TurnStarted) EventMethod() string { return "turn/started" }

// TurnCompleted is emitted when the turn reaches a terminal state. Status
// is "success" | "failed" | "cancelled". Usage is the final per-turn
// accounting.
type TurnCompleted struct {
	ThreadID string     `json:"thread_id"`
	TurnID   string     `json:"turn_id"`
	Status   string     `json:"status"`
	Usage    TokenUsage `json:"usage"`
}

func (*TurnCompleted) isThreadEvent()      {}
func (*TurnCompleted) EventMethod() string { return "turn/completed" }

// TurnFailed is emitted if the server reports a terminal turn failure
// through a dedicated method (as opposed to TurnCompleted{Status:"failed"}).
// Both forms are observed in the wild.
type TurnFailed struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

func (*TurnFailed) isThreadEvent()      {}
func (*TurnFailed) EventMethod() string { return "turn/failed" }

// ItemStarted is emitted when the server begins emitting a new item inside
// the current turn. Item is a concrete ThreadItem (see items.go).
type ItemStarted struct {
	ThreadID string     `json:"thread_id"`
	TurnID   string     `json:"turn_id"`
	ItemID   string     `json:"item_id"`
	Item     ThreadItem `json:"item"`
}

func (*ItemStarted) isThreadEvent()      {}
func (*ItemStarted) EventMethod() string { return "item/started" }

// ItemUpdated is emitted for each delta of a streaming item. Delta is a
// concrete ItemDelta (see items.go).
type ItemUpdated struct {
	ThreadID string    `json:"thread_id"`
	TurnID   string    `json:"turn_id"`
	ItemID   string    `json:"item_id"`
	Delta    ItemDelta `json:"delta"`
}

func (*ItemUpdated) isThreadEvent()      {}
func (*ItemUpdated) EventMethod() string { return "item/updated" }

// ItemCompleted is emitted when an item reaches its terminal state. The
// Item payload is the fully-populated version (e.g., a complete
// AgentMessage, or a CommandExecution with exit_code/stdout/stderr).
type ItemCompleted struct {
	ThreadID string     `json:"thread_id"`
	TurnID   string     `json:"turn_id"`
	ItemID   string     `json:"item_id"`
	Item     ThreadItem `json:"item"`
}

func (*ItemCompleted) isThreadEvent()      {}
func (*ItemCompleted) EventMethod() string { return "item/completed" }

// TokenUsageUpdated is emitted periodically with a running token-usage
// snapshot. This is NOT the same as TurnCompleted.Usage (which is the
// turn's final accounting) — this one reports the thread-cumulative total.
type TokenUsageUpdated struct {
	ThreadID string     `json:"thread_id"`
	Usage    TokenUsage `json:"usage"`
}

func (*TokenUsageUpdated) isThreadEvent()      {}
func (*TokenUsageUpdated) EventMethod() string { return "thread/tokenUsage/updated" }

// ErrorEvent is emitted for any non-fatal server-side error that the server
// wants the client to observe without terminating the turn.
type ErrorEvent struct {
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message"`
	Context json.RawMessage `json:"context,omitempty"`
}

func (*ErrorEvent) isThreadEvent()      {}
func (*ErrorEvent) EventMethod() string { return "error" }

// UnknownEvent is the forward-compat hatch for method names the parser
// doesn't recognize. Callers that care MUST type-switch on UnknownEvent
// to inspect the raw payload.
type UnknownEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (*UnknownEvent) isThreadEvent()        {}
func (u *UnknownEvent) EventMethod() string { return u.Method }

// --- v0.2.0 expansion: thread lifecycle events ---

// ThreadArchived is emitted when a thread is archived on the server.
// Wire method: "thread/archived".
type ThreadArchived struct {
	ThreadID string `json:"thread_id"`
}

func (*ThreadArchived) isThreadEvent()      {}
func (*ThreadArchived) EventMethod() string { return "thread/archived" }

// ThreadUnarchived is emitted when a thread is restored from archive.
// Wire method: "thread/unarchived".
type ThreadUnarchived struct {
	ThreadID string `json:"thread_id"`
}

func (*ThreadUnarchived) isThreadEvent()      {}
func (*ThreadUnarchived) EventMethod() string { return "thread/unarchived" }

// ThreadClosed is emitted when the server closes its side of a thread.
// Wire method: "thread/closed".
type ThreadClosed struct {
	ThreadID string `json:"thread_id"`
}

func (*ThreadClosed) isThreadEvent()      {}
func (*ThreadClosed) EventMethod() string { return "thread/closed" }

// ThreadNameUpdated is emitted when the thread's name changes. ThreadName
// is nil when the name was cleared.
// Wire method: "thread/name/updated".
type ThreadNameUpdated struct {
	ThreadID   string  `json:"thread_id"`
	ThreadName *string `json:"thread_name,omitempty"`
}

func (*ThreadNameUpdated) isThreadEvent()      {}
func (*ThreadNameUpdated) EventMethod() string { return "thread/name/updated" }

// ThreadStatusChanged is emitted when the server's status for a thread
// transitions (e.g., idle → running → blocked). Status is the server-side
// status string.
// Wire method: "thread/status/changed".
type ThreadStatusChanged struct {
	ThreadID string          `json:"thread_id"`
	Status   json.RawMessage `json:"status"`
}

func (*ThreadStatusChanged) isThreadEvent()      {}
func (*ThreadStatusChanged) EventMethod() string { return "thread/status/changed" }

// ContextCompacted is emitted when the server summarizes conversation
// history to free context-window space. Supersedes v0.1.0's
// CompactionEvent.
// Wire method: "thread/compacted".
type ContextCompacted struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
}

func (*ContextCompacted) isThreadEvent()      {}
func (*ContextCompacted) EventMethod() string { return "thread/compacted" }

// --- v0.2.0 expansion: realtime-conversation events ---

// ThreadRealtimeStarted is emitted when the server begins a realtime
// (voice/audio) conversation. Wire method: "thread/realtime/started".
type ThreadRealtimeStarted struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeStarted) isThreadEvent()      {}
func (*ThreadRealtimeStarted) EventMethod() string { return "thread/realtime/started" }

// ThreadRealtimeClosed is emitted when a realtime conversation terminates.
// Wire method: "thread/realtime/closed".
type ThreadRealtimeClosed struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeClosed) isThreadEvent()      {}
func (*ThreadRealtimeClosed) EventMethod() string { return "thread/realtime/closed" }

// ThreadRealtimeError is emitted on realtime-conversation errors.
// Wire method: "thread/realtime/error".
type ThreadRealtimeError struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeError) isThreadEvent()      {}
func (*ThreadRealtimeError) EventMethod() string { return "thread/realtime/error" }

// ThreadRealtimeItemAdded is emitted when a realtime conversation adds a
// new item (audio/text fragment). Wire method: "thread/realtime/itemAdded".
type ThreadRealtimeItemAdded struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeItemAdded) isThreadEvent()      {}
func (*ThreadRealtimeItemAdded) EventMethod() string { return "thread/realtime/itemAdded" }

// ThreadRealtimeOutputAudioDelta is emitted as the server streams audio
// output chunks during a realtime conversation.
// Wire method: "thread/realtime/outputAudio/delta".
type ThreadRealtimeOutputAudioDelta struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeOutputAudioDelta) isThreadEvent() {}
func (*ThreadRealtimeOutputAudioDelta) EventMethod() string {
	return "thread/realtime/outputAudio/delta"
}

// ThreadRealtimeSdp carries WebRTC session-description exchanges.
// Wire method: "thread/realtime/sdp".
type ThreadRealtimeSdp struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeSdp) isThreadEvent()      {}
func (*ThreadRealtimeSdp) EventMethod() string { return "thread/realtime/sdp" }

// ThreadRealtimeTranscriptDelta streams the transcript of a realtime
// conversation. Wire method: "thread/realtime/transcript/delta".
type ThreadRealtimeTranscriptDelta struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeTranscriptDelta) isThreadEvent() {}
func (*ThreadRealtimeTranscriptDelta) EventMethod() string {
	return "thread/realtime/transcript/delta"
}

// ThreadRealtimeTranscriptDone is emitted when transcription for a
// realtime turn completes. Wire method: "thread/realtime/transcript/done".
type ThreadRealtimeTranscriptDone struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeTranscriptDone) isThreadEvent() {}
func (*ThreadRealtimeTranscriptDone) EventMethod() string {
	return "thread/realtime/transcript/done"
}

// --- v0.2.0 expansion: turn-scoped events ---

// TurnDiffUpdated is emitted when the server updates the aggregated diff
// for a turn. Wire method: "turn/diff/updated".
type TurnDiffUpdated struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
	Diff     string `json:"diff"`
}

func (*TurnDiffUpdated) isThreadEvent()      {}
func (*TurnDiffUpdated) EventMethod() string { return "turn/diff/updated" }

// TurnPlanUpdated is emitted when the server updates the plan for a turn.
// Plan is the raw plan payload (array of steps); Explanation is optional
// context the server included.
// Wire method: "turn/plan/updated".
type TurnPlanUpdated struct {
	ThreadID    string          `json:"thread_id"`
	TurnID      string          `json:"turn_id"`
	Plan        json.RawMessage `json:"plan"`
	Explanation *string         `json:"explanation,omitempty"`
}

func (*TurnPlanUpdated) isThreadEvent()      {}
func (*TurnPlanUpdated) EventMethod() string { return "turn/plan/updated" }

// --- v0.2.0 expansion: guardian auto-approval review events ---

// ItemGuardianApprovalReviewStarted is emitted when the server delegates
// an approval decision to an automated guardian subagent.
// Wire method: "item/autoApprovalReview/started".
type ItemGuardianApprovalReviewStarted struct {
	ThreadID     string          `json:"thread_id"`
	TurnID       string          `json:"turn_id"`
	ReviewID     string          `json:"review_id"`
	TargetItemID *string         `json:"target_item_id,omitempty"`
	Action       json.RawMessage `json:"action"`
	Review       json.RawMessage `json:"review"`
}

func (*ItemGuardianApprovalReviewStarted) isThreadEvent() {}
func (*ItemGuardianApprovalReviewStarted) EventMethod() string {
	return "item/autoApprovalReview/started"
}

// ItemGuardianApprovalReviewCompleted is emitted when the guardian
// subagent reaches a decision. DecisionSource indicates whether the
// decision came from policy rules or the subagent LLM.
// Wire method: "item/autoApprovalReview/completed".
type ItemGuardianApprovalReviewCompleted struct {
	ThreadID       string          `json:"thread_id"`
	TurnID         string          `json:"turn_id"`
	ReviewID       string          `json:"review_id"`
	TargetItemID   *string         `json:"target_item_id,omitempty"`
	Action         json.RawMessage `json:"action"`
	Review         json.RawMessage `json:"review"`
	DecisionSource json.RawMessage `json:"decision_source"`
}

func (*ItemGuardianApprovalReviewCompleted) isThreadEvent() {}
func (*ItemGuardianApprovalReviewCompleted) EventMethod() string {
	return "item/autoApprovalReview/completed"
}

// --- v0.2.0 expansion: MCP events ---

// MCPServerStartupStatusUpdated is emitted as each MCP server moves through
// its startup lifecycle (starting → connected | error).
// Wire method: "mcpServer/startupStatus/updated".
type MCPServerStartupStatusUpdated struct {
	Name   string          `json:"name"`
	Status json.RawMessage `json:"status"`
	Error  *string         `json:"error,omitempty"`
}

func (*MCPServerStartupStatusUpdated) isThreadEvent() {}
func (*MCPServerStartupStatusUpdated) EventMethod() string {
	return "mcpServer/startupStatus/updated"
}

// MCPServerOAuthLoginCompleted is emitted when an MCP server's OAuth flow
// finishes. Wire method: "mcpServer/oauthLogin/completed".
type MCPServerOAuthLoginCompleted struct {
	Name    string  `json:"name"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

func (*MCPServerOAuthLoginCompleted) isThreadEvent() {}
func (*MCPServerOAuthLoginCompleted) EventMethod() string {
	return "mcpServer/oauthLogin/completed"
}

// --- v0.2.0 expansion: account + model events ---

// AccountLoginCompleted is emitted when a login flow finishes.
// Wire method: "account/login/completed".
type AccountLoginCompleted struct {
	Success bool    `json:"success"`
	LoginID *string `json:"login_id,omitempty"`
	Error   *string `json:"error,omitempty"`
}

func (*AccountLoginCompleted) isThreadEvent()      {}
func (*AccountLoginCompleted) EventMethod() string { return "account/login/completed" }

// AccountRateLimitsUpdated is emitted when the server pushes a fresh rate-
// limit snapshot. RateLimits is the raw snapshot payload.
// Wire method: "account/rateLimits/updated".
type AccountRateLimitsUpdated struct {
	RateLimits json.RawMessage `json:"rate_limits"`
}

func (*AccountRateLimitsUpdated) isThreadEvent()      {}
func (*AccountRateLimitsUpdated) EventMethod() string { return "account/rateLimits/updated" }

// AccountUpdated is emitted when the authenticated account's metadata
// changes (plan type, auth mode).
// Wire method: "account/updated".
type AccountUpdated struct {
	AuthMode json.RawMessage `json:"auth_mode,omitempty"`
	PlanType json.RawMessage `json:"plan_type,omitempty"`
}

func (*AccountUpdated) isThreadEvent()      {}
func (*AccountUpdated) EventMethod() string { return "account/updated" }

// ModelRerouted is emitted when the server reroutes a turn to a different
// model (e.g., fast-mode fallback, rate-limit rerouting).
// Wire method: "model/rerouted".
type ModelRerouted struct {
	ThreadID  string          `json:"thread_id"`
	TurnID    string          `json:"turn_id"`
	FromModel string          `json:"from_model"`
	ToModel   string          `json:"to_model"`
	Reason    json.RawMessage `json:"reason"`
}

func (*ModelRerouted) isThreadEvent()      {}
func (*ModelRerouted) EventMethod() string { return "model/rerouted" }

// --- v0.2.0 expansion: system / filesystem events ---

// ConfigWarning is emitted when codex detects a suspect config value.
// Wire method: "configWarning".
type ConfigWarning struct {
	Summary string          `json:"summary"`
	Details *string         `json:"details,omitempty"`
	Path    *string         `json:"path,omitempty"`
	Range   json.RawMessage `json:"range,omitempty"`
}

func (*ConfigWarning) isThreadEvent()      {}
func (*ConfigWarning) EventMethod() string { return "configWarning" }

// DeprecationNotice carries a runtime deprecation message from the server.
// Wire method: "deprecationNotice".
type DeprecationNotice struct {
	Summary string  `json:"summary"`
	Details *string `json:"details,omitempty"`
}

func (*DeprecationNotice) isThreadEvent()      {}
func (*DeprecationNotice) EventMethod() string { return "deprecationNotice" }

// FsChanged is emitted when codex's filesystem watcher detects changes.
// Wire method: "fs/changed".
type FsChanged struct {
	WatchID      string   `json:"watch_id"`
	ChangedPaths []string `json:"changed_paths"`
}

func (*FsChanged) isThreadEvent()      {}
func (*FsChanged) EventMethod() string { return "fs/changed" }

// SkillsChanged is emitted when the server's skill registry changes.
// Wire method: "skills/changed". Params are not currently exposed (empty
// required set on the schema).
type SkillsChanged struct{}

func (*SkillsChanged) isThreadEvent()      {}
func (*SkillsChanged) EventMethod() string { return "skills/changed" }

// AppListUpdated is emitted when the server's list of available apps
// changes. Wire method: "app/list/updated".
type AppListUpdated struct {
	Data json.RawMessage `json:"data"`
}

func (*AppListUpdated) isThreadEvent()      {}
func (*AppListUpdated) EventMethod() string { return "app/list/updated" }

// ServerRequestResolved is emitted when a previously-pending server-
// initiated request (e.g., an approval) is resolved.
// Wire method: "serverRequest/resolved".
type ServerRequestResolved struct {
	ThreadID  string          `json:"thread_id"`
	RequestID json.RawMessage `json:"request_id"`
}

func (*ServerRequestResolved) isThreadEvent()      {}
func (*ServerRequestResolved) EventMethod() string { return "serverRequest/resolved" }

// --- v0.2.0 expansion: Windows platform events ---

// WindowsWorldWritableWarning is emitted when codex detects world-writable
// paths in the workspace on Windows. Wire method:
// "windows/worldWritableWarning".
type WindowsWorldWritableWarning struct {
	ExtraCount  int      `json:"extra_count"`
	FailedScan  bool     `json:"failed_scan"`
	SamplePaths []string `json:"sample_paths"`
}

func (*WindowsWorldWritableWarning) isThreadEvent() {}
func (*WindowsWorldWritableWarning) EventMethod() string {
	return "windows/worldWritableWarning"
}

// WindowsSandboxSetupCompleted is emitted when Windows sandbox
// initialization finishes. Wire method: "windowsSandbox/setupCompleted".
type WindowsSandboxSetupCompleted struct {
	Success bool            `json:"success"`
	Mode    json.RawMessage `json:"mode"`
	Error   *string         `json:"error,omitempty"`
}

func (*WindowsSandboxSetupCompleted) isThreadEvent() {}
func (*WindowsSandboxSetupCompleted) EventMethod() string {
	return "windowsSandbox/setupCompleted"
}

// --- v0.2.0 expansion: fuzzy file search events ---

// FuzzyFileSearchSessionUpdated is emitted during a fuzzy file search
// session. Wire method: "fuzzyFileSearch/sessionUpdated".
type FuzzyFileSearchSessionUpdated struct {
	Params json.RawMessage `json:"params,omitempty"`
}

func (*FuzzyFileSearchSessionUpdated) isThreadEvent() {}
func (*FuzzyFileSearchSessionUpdated) EventMethod() string {
	return "fuzzyFileSearch/sessionUpdated"
}

// FuzzyFileSearchSessionCompleted is emitted when a fuzzy file search
// session completes. Wire method: "fuzzyFileSearch/sessionCompleted".
type FuzzyFileSearchSessionCompleted struct {
	Params json.RawMessage `json:"params,omitempty"`
}

func (*FuzzyFileSearchSessionCompleted) isThreadEvent() {}
func (*FuzzyFileSearchSessionCompleted) EventMethod() string {
	return "fuzzyFileSearch/sessionCompleted"
}
