package types

import "encoding/json"

// ThreadItem is the interface implemented by every concrete item type
// returned in ItemStarted / ItemCompleted events. Items are the granular
// actions the agent performs: messages, commands, file edits, MCP tool
// calls, web searches, memory ops, plans, reasoning.
//
// The interface is sealed via the unexported isThreadItem() marker — only
// types declared in this package satisfy it. This prevents accidental
// shape drift and makes exhaustive switch-handling tractable.
type ThreadItem interface {
	isThreadItem()
	// ItemType returns the wire-level discriminator string (e.g.,
	// "agent_message", "command_execution"). UnknownItem returns its Type
	// field.
	ItemType() string
}

// AgentMessage is a text response from the model.
type AgentMessage struct {
	Content string `json:"content"`
}

func (*AgentMessage) isThreadItem()    {}
func (*AgentMessage) ItemType() string { return "agent_message" }

// UserMessage is user input submitted to the thread (echoed back in the
// transcript).
type UserMessage struct {
	Content string `json:"content"`
}

func (*UserMessage) isThreadItem()    {}
func (*UserMessage) ItemType() string { return "user_message" }

// CommandExecution reports a shell command the agent ran (or attempted to
// run — see Status).
type CommandExecution struct {
	Command    string `json:"command"`
	Cwd        string `json:"working_directory,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Status     string `json:"status,omitempty"` // "success" | "failed" | "timed_out" | "denied"
	DurationMs int64  `json:"duration_ms,omitempty"`
}

func (*CommandExecution) isThreadItem()    {}
func (*CommandExecution) ItemType() string { return "command_execution" }

// FileChange describes a single-file edit performed by the agent.
type FileChange struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // "create" | "modify" | "delete"
	Diff      string `json:"diff,omitempty"`
	Status    string `json:"status,omitempty"` // "success" | "failed" | "denied"
}

func (*FileChange) isThreadItem()    {}
func (*FileChange) ItemType() string { return "file_change" }

// MCPToolCall captures a call the agent made to a Model Context Protocol
// tool.
type MCPToolCall struct {
	ServerName string          `json:"server_name"`
	ToolName   string          `json:"tool_name"`
	Input      json.RawMessage `json:"input,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Status     string          `json:"status,omitempty"` // "success" | "failed" | "denied"
	ErrorText  string          `json:"error,omitempty"`
}

func (*MCPToolCall) isThreadItem()    {}
func (*MCPToolCall) ItemType() string { return "mcp_tool_call" }

// WebSearch records a search the agent performed.
type WebSearch struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results,omitempty"`
}

// WebSearchResult is a single result row in a WebSearch.
type WebSearchResult struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

func (*WebSearch) isThreadItem()    {}
func (*WebSearch) ItemType() string { return "web_search" }

// MemoryRead captures a lookup against the knowledge store.
type MemoryRead struct {
	Query  string `json:"query"`
	Result string `json:"result,omitempty"`
}

func (*MemoryRead) isThreadItem()    {}
func (*MemoryRead) ItemType() string { return "memory_read" }

// MemoryWrite captures a write to the knowledge store.
type MemoryWrite struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (*MemoryWrite) isThreadItem()    {}
func (*MemoryWrite) ItemType() string { return "memory_write" }

// Plan is a structured plan the agent produced (via the /plan tool or
// explicit plan() call).
type Plan struct {
	Content string `json:"content"`
	Status  string `json:"status,omitempty"` // "active" | "completed" | "abandoned"
}

func (*Plan) isThreadItem()    {}
func (*Plan) ItemType() string { return "plan" }

// Reasoning is extended thinking output — usually emitted via item/updated
// deltas rather than a single item/completed payload.
//
// Summary and Content are preserved as raw JSON elements because the codex
// wire format uses variable shapes (empty arrays during streaming,
// structured parts when completed). Callers interested in reasoning text
// should unmarshal each element into their preferred shape or use the
// reasoning_delta events which carry flat text chunks.
type Reasoning struct {
	ID      string            `json:"id,omitempty"`
	Summary []json.RawMessage `json:"summary,omitempty"`
	Content []json.RawMessage `json:"content,omitempty"`
}

func (*Reasoning) isThreadItem()    {}
func (*Reasoning) ItemType() string { return "reasoning" }

// UnknownItem is emitted when the parser encounters an item.type it does
// not recognize. The Type field carries the wire discriminator and Raw
// holds the complete item payload so callers can inspect it.
//
// This is the forward-compat hatch: the SDK keeps working when codex
// introduces a new item subtype. Callers that care MUST type-switch
// explicitly on UnknownItem to handle it.
type UnknownItem struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

func (*UnknownItem) isThreadItem()      {}
func (u *UnknownItem) ItemType() string { return u.Type }

// ItemDelta is the interface implemented by partial updates emitted during
// an item's lifecycle (between item/started and item/completed). Deltas
// vary per item type.
type ItemDelta interface {
	isItemDelta()
	// DeltaType returns the wire-level discriminator string (e.g.,
	// "agent_message_delta", "command_output_delta").
	DeltaType() string
}

// AgentMessageDelta is a streaming chunk of agent message text.
type AgentMessageDelta struct {
	TextChunk string `json:"text_chunk"`
}

func (*AgentMessageDelta) isItemDelta()      {}
func (*AgentMessageDelta) DeltaType() string { return "agent_message_delta" }

// ReasoningDelta is a streaming chunk of extended-thinking text.
type ReasoningDelta struct {
	TextChunk    string `json:"text_chunk,omitempty"`
	SummaryChunk string `json:"summary_chunk,omitempty"`
}

func (*ReasoningDelta) isItemDelta()      {}
func (*ReasoningDelta) DeltaType() string { return "reasoning_delta" }

// CommandOutputDelta is a streaming chunk of a command_execution's stdout
// or stderr.
type CommandOutputDelta struct {
	StdoutChunk string `json:"stdout_chunk,omitempty"`
	StderrChunk string `json:"stderr_chunk,omitempty"`
}

func (*CommandOutputDelta) isItemDelta()      {}
func (*CommandOutputDelta) DeltaType() string { return "command_output_delta" }

// FileChangeOutputDelta is a streaming chunk of a file_change diff.
type FileChangeOutputDelta struct {
	DiffChunk string `json:"diff_chunk"`
}

func (*FileChangeOutputDelta) isItemDelta()      {}
func (*FileChangeOutputDelta) DeltaType() string { return "file_change_output_delta" }

// MCPToolCallProgress is a status update emitted during a long-running MCP
// tool call.
type MCPToolCallProgress struct {
	Stage    string `json:"stage,omitempty"`
	Progress int    `json:"progress,omitempty"`
}

func (*MCPToolCallProgress) isItemDelta()      {}
func (*MCPToolCallProgress) DeltaType() string { return "mcp_tool_call_progress" }

// UnknownDelta is the forward-compat hatch for unrecognized delta types.
type UnknownDelta struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

func (*UnknownDelta) isItemDelta()        {}
func (u *UnknownDelta) DeltaType() string { return u.Type }
