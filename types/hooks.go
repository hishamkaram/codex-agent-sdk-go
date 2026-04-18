package types

import (
	"context"
	"encoding/json"
)

// --- Hook enum types (match v2 schema 1:1) ---

// HookEventName is one of the five values Codex emits for lifecycle hooks.
// Codex 0.121.0's HookEventName enum fixes the set — new values will
// require a v0.3.0 SDK release.
type HookEventName string

const (
	HookPreToolUse       HookEventName = "preToolUse"
	HookPostToolUse      HookEventName = "postToolUse"
	HookSessionStart     HookEventName = "sessionStart"
	HookUserPromptSubmit HookEventName = "userPromptSubmit"
	HookStop             HookEventName = "stop"
)

// HookHandlerType describes WHICH upstream handler Codex invoked. v0.2.0
// SDK only supports registering "command" handlers (via the shim binary);
// "prompt" and "agent" handlers stay config-file-only.
type HookHandlerType string

const (
	HookHandlerCommand HookHandlerType = "command"
	HookHandlerPrompt  HookHandlerType = "prompt"
	HookHandlerAgent   HookHandlerType = "agent"
)

// HookExecutionMode says whether the hook blocks the turn (sync) or fires
// alongside it (async).
type HookExecutionMode string

const (
	HookExecutionSync  HookExecutionMode = "sync"
	HookExecutionAsync HookExecutionMode = "async"
)

// HookScope indicates whether the hook applies to a single turn or the
// whole thread.
type HookScope string

const (
	HookScopeThread HookScope = "thread"
	HookScopeTurn   HookScope = "turn"
)

// HookRunStatus is the life-cycle state of a single hook invocation.
type HookRunStatus string

const (
	HookRunStatusRunning   HookRunStatus = "running"
	HookRunStatusCompleted HookRunStatus = "completed"
	HookRunStatusFailed    HookRunStatus = "failed"
	HookRunStatusBlocked   HookRunStatus = "blocked"
	HookRunStatusStopped   HookRunStatus = "stopped"
)

// HookOutputEntryKind categorizes an entry the hook wrote.
type HookOutputEntryKind string

const (
	HookOutputKindWarning  HookOutputEntryKind = "warning"
	HookOutputKindStop     HookOutputEntryKind = "stop"
	HookOutputKindFeedback HookOutputEntryKind = "feedback"
	HookOutputKindContext  HookOutputEntryKind = "context"
	HookOutputKindError    HookOutputEntryKind = "error"
)

// HookOutputEntry is one structured piece of output a hook emitted.
type HookOutputEntry struct {
	Kind HookOutputEntryKind `json:"kind"`
	Text string              `json:"text"`
}

// HookRunSummary mirrors the v2 schema `HookRunSummary` type 1:1.
type HookRunSummary struct {
	ID            string            `json:"id"`
	EventName     HookEventName     `json:"eventName"`
	HandlerType   HookHandlerType   `json:"handlerType"`
	ExecutionMode HookExecutionMode `json:"executionMode"`
	Scope         HookScope         `json:"scope"`
	SourcePath    string            `json:"sourcePath"`
	Entries       []HookOutputEntry `json:"entries"`
	Status        HookRunStatus     `json:"status"`
	StatusMessage *string           `json:"statusMessage,omitempty"`
	DisplayOrder  int64             `json:"displayOrder"`
	StartedAt     int64             `json:"startedAt"`
	CompletedAt   *int64            `json:"completedAt,omitempty"`
	DurationMs    *int64            `json:"durationMs,omitempty"`
}

// --- Observer events: HookStarted / HookCompleted ---
// Implement types.ThreadEvent.

// HookStarted is emitted when codex begins running a hook handler.
// Wire method: "hook/started".
type HookStarted struct {
	ThreadID string         `json:"thread_id"`
	TurnID   *string        `json:"turn_id,omitempty"`
	Run      HookRunSummary `json:"run"`
}

func (*HookStarted) isThreadEvent()      {}
func (*HookStarted) EventMethod() string { return "hook/started" }

// HookCompleted is emitted when a hook handler finishes (either
// successfully, with a block, or with an error).
// Wire method: "hook/completed".
type HookCompleted struct {
	ThreadID string         `json:"thread_id"`
	TurnID   *string        `json:"turn_id,omitempty"`
	Run      HookRunSummary `json:"run"`
}

func (*HookCompleted) isThreadEvent()      {}
func (*HookCompleted) EventMethod() string { return "hook/completed" }

// --- Callback API (used by Phase 4's shim bridge) ---

// HookInput is the JSON payload codex writes to a hook subprocess's stdin.
// The shim bridge deserializes this and hands it to the user's
// HookHandler. Field presence depends on HookEventName — check the
// EventName first and inspect the appropriate fields.
//
// The Raw field holds the full JSON payload unmodified for callers that
// need fields the SDK hasn't typed yet.
//
// HookEventName normalization: codex writes the event name in PascalCase
// on stdin (e.g. "PreToolUse") but the SDK's HookEventName constants are
// camelCase (e.g. HookPreToolUse = "preToolUse") to match the wire
// notification body. UnmarshalJSON converts PascalCase → camelCase
// transparently so callers always see the camelCase form.
type HookInput struct {
	HookEventName        HookEventName   `json:"hook_event_name"`
	SessionID            string          `json:"session_id,omitempty"`
	TranscriptPath       string          `json:"transcript_path,omitempty"`
	Cwd                  string          `json:"cwd,omitempty"`
	PermissionMode       string          `json:"permission_mode,omitempty"`
	Model                string          `json:"model,omitempty"`
	TurnID               *string         `json:"turn_id,omitempty"`
	Prompt               *string         `json:"prompt,omitempty"`                 // userPromptSubmit / sessionStart
	LastAssistantMessage *string         `json:"last_assistant_message,omitempty"` // stop
	ToolName             string          `json:"tool_name,omitempty"`              // preToolUse / postToolUse
	ToolInput            json.RawMessage `json:"tool_input,omitempty"`             // preToolUse
	ToolResult           json.RawMessage `json:"tool_result,omitempty"`            // postToolUse
	ToolUseID            string          `json:"tool_use_id,omitempty"`            // preToolUse / postToolUse
	Source               *string         `json:"source,omitempty"`                 // sessionStart: "startup"|"resume"|"clear"
	Raw                  json.RawMessage `json:"-"`                                // full payload for escape-hatch inspection
}

// UnmarshalJSON parses the hook stdin payload, normalizing the
// hook_event_name from codex's PascalCase wire form to the SDK's
// camelCase HookEventName constants. Unknown event names pass through
// verbatim.
func (h *HookInput) UnmarshalJSON(data []byte) error {
	type alias HookInput
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	a.HookEventName = NormalizeHookEventName(a.HookEventName)
	*h = HookInput(a)
	return nil
}

// NormalizeHookEventName converts codex's PascalCase hook_event_name
// values ("PreToolUse", "PostToolUse", "SessionStart", "UserPromptSubmit",
// "Stop") to the SDK's camelCase HookEventName constants. Already-camelCase
// values pass through unchanged. Unknown names pass through verbatim so
// future codex versions don't break parsing.
func NormalizeHookEventName(name HookEventName) HookEventName {
	switch name {
	case "PreToolUse":
		return HookPreToolUse
	case "PostToolUse":
		return HookPostToolUse
	case "SessionStart":
		return HookSessionStart
	case "UserPromptSubmit":
		return HookUserPromptSubmit
	case "Stop":
		return HookStop
	}
	return name
}

// HookDecision is the value a HookHandler returns. Exactly one of the
// concrete types (HookAllow, HookDeny, HookAsk) satisfies this interface.
// The shim serializes the decision into the format codex's hook stdout
// contract expects and exits with the appropriate code.
type HookDecision interface {
	isHookDecision()
}

// HookAllow tells codex to proceed with the pending action. Applies to
// preToolUse, postToolUse, sessionStart, userPromptSubmit, and stop.
//
// UpdatedInput is preToolUse-only: when non-nil, codex swaps in your
// modified tool_input before executing. Use for command rewriting or
// argument sanitization. Ignored for other hook events.
//
// AdditionalContext and SystemMessage are applicable to sessionStart,
// userPromptSubmit, and stop — they inject extra context into the model's
// view before the next turn.
type HookAllow struct {
	SystemMessage     *string         `json:"systemMessage,omitempty"`
	AdditionalContext *string         `json:"additionalContext,omitempty"`
	UpdatedInput      json.RawMessage `json:"updatedInput,omitempty"`
}

func (HookAllow) isHookDecision() {}

// HookDeny tells codex to block the pending action. Applies to preToolUse
// (command won't run), userPromptSubmit (prompt rejected), stop
// (continuation blocked).
type HookDeny struct {
	Reason         string  `json:"reason"`
	SuppressOutput bool    `json:"suppressOutput,omitempty"`
	SystemMessage  *string `json:"systemMessage,omitempty"`
}

func (HookDeny) isHookDecision() {}

// HookAsk falls back to codex's normal approval flow for preToolUse —
// users will see the command-execution approval prompt and the SDK's
// ApprovalCallback will fire. No effect for other hook events (treated
// as HookAllow).
type HookAsk struct {
	Reason string `json:"reason,omitempty"`
}

func (HookAsk) isHookDecision() {}

// HookHandler is the signature of a user-supplied hook callback. It runs
// inside the SDK process (not the hook subprocess) and returns a decision
// the shim sends back to codex.
//
// CRITICAL: the handler MUST return promptly (default timeout 30s,
// override via CodexOptions.WithHookTimeout). Never call Thread.Run,
// Thread.RunStreamed, or any SDK operation that takes the dispatcher
// lock — that will deadlock. Do user interaction from a separate
// goroutine and feed the result through a channel if needed.
type HookHandler func(ctx context.Context, in HookInput) HookDecision

// DefaultAllowHookHandler is a no-op handler that returns HookAllow{} for
// every hook. Useful for tests and for clients who want hooks to fire
// (for observability) without changing any behavior.
func DefaultAllowHookHandler(ctx context.Context, in HookInput) HookDecision {
	_ = ctx
	_ = in
	return HookAllow{}
}
