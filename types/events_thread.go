package types

import "encoding/json"

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

// ThreadDeleted is emitted after a thread is permanently deleted.
// Wire method: "thread/deleted".
type ThreadDeleted struct {
	ThreadID string `json:"thread_id"`
}

func (*ThreadDeleted) isThreadEvent()      {}
func (*ThreadDeleted) EventMethod() string { return "thread/deleted" }

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
// transitions (e.g., idle -> running -> blocked). Status is the server-side
// status string.
// Wire method: "thread/status/changed".
type ThreadStatusChanged struct {
	ThreadID string          `json:"thread_id"`
	Status   json.RawMessage `json:"status"`
}

func (*ThreadStatusChanged) isThreadEvent()      {}
func (*ThreadStatusChanged) EventMethod() string { return "thread/status/changed" }

// ThreadSettingsUpdated is emitted when the server updates thread-scoped
// settings. ThreadSettings is raw because upstream settings are broad and may
// grow independently from SDK behavior.
// Wire method: "thread/settings/updated".
type ThreadSettingsUpdated struct {
	ThreadID       string          `json:"thread_id"`
	ThreadSettings json.RawMessage `json:"thread_settings"`
}

func (*ThreadSettingsUpdated) isThreadEvent()      {}
func (*ThreadSettingsUpdated) EventMethod() string { return "thread/settings/updated" }

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

// ThreadGoalUpdated carries the current goal state for a thread.
// Wire method: "thread/goal/updated".
type ThreadGoalUpdated struct {
	ThreadID string          `json:"thread_id"`
	TurnID   *string         `json:"turn_id,omitempty"`
	Goal     json.RawMessage `json:"goal"`
}

func (*ThreadGoalUpdated) isThreadEvent()      {}
func (*ThreadGoalUpdated) EventMethod() string { return "thread/goal/updated" }

// ThreadGoalCleared is emitted when a thread goal is cleared.
// Wire method: "thread/goal/cleared".
type ThreadGoalCleared struct {
	ThreadID string `json:"thread_id"`
}

func (*ThreadGoalCleared) isThreadEvent()      {}
func (*ThreadGoalCleared) EventMethod() string { return "thread/goal/cleared" }
