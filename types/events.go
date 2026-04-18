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

// CompactionEvent is emitted when the server summarizes conversation
// history to free context-window space.
type CompactionEvent struct {
	ThreadID    string `json:"thread_id"`
	TokensFreed int64  `json:"tokens_freed,omitempty"`
	Strategy    string `json:"strategy,omitempty"` // "handoff_summary" | ...
}

func (*CompactionEvent) isThreadEvent()      {}
func (*CompactionEvent) EventMethod() string { return "compaction_event" }

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
