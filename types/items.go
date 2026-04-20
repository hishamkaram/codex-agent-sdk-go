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
//
// Field names and JSON tags match the codex server's camelCase wire
// format (verified against CLI 0.121.0 transcripts).
type ThreadItem interface {
	isThreadItem()
	// ItemType returns the wire-level discriminator string (e.g.,
	// "agentMessage", "commandExecution"). UnknownItem returns its
	// Type field.
	ItemType() string
}

// AgentMessage is a text response from the model. Wire discriminator:
// "agentMessage". The payload carries a flat "text" field plus metadata.
type AgentMessage struct {
	ID             string          `json:"id,omitempty"`
	Text           string          `json:"text"`
	Phase          string          `json:"phase,omitempty"` // "final_answer" | …
	MemoryCitation json.RawMessage `json:"memoryCitation,omitempty"`
}

func (*AgentMessage) isThreadItem()    {}
func (*AgentMessage) ItemType() string { return "agentMessage" }

// UserMessage is user input echoed into the transcript. Content is an
// ARRAY of parts (text, localImage, etc.) — the server normalizes the
// client's input.
type UserMessage struct {
	ID      string            `json:"id,omitempty"`
	Content []UserMessagePart `json:"content,omitempty"`
}

// UserMessagePart is one element of a UserMessage.Content array.
type UserMessagePart struct {
	Type         string          `json:"type"` // "text" | "localImage" | …
	Text         string          `json:"text,omitempty"`
	Path         string          `json:"path,omitempty"`
	TextElements json.RawMessage `json:"text_elements,omitempty"`
}

func (*UserMessage) isThreadItem()    {}
func (*UserMessage) ItemType() string { return "userMessage" }

// CommandExecution reports a shell command the agent ran (or attempted to
// run — see Status). Wire discriminator: "commandExecution".
type CommandExecution struct {
	ID               string          `json:"id,omitempty"`
	Command          string          `json:"command"`
	Cwd              string          `json:"cwd,omitempty"`
	Source           string          `json:"source,omitempty"` // "agent" | "user"
	ProcessID        string          `json:"processId,omitempty"`
	Status           string          `json:"status,omitempty"` // "inProgress" | "success" | "failed" | "denied"
	ExitCode         *int            `json:"exitCode,omitempty"`
	AggregatedOutput string          `json:"aggregatedOutput,omitempty"`
	DurationMs       *int64          `json:"durationMs,omitempty"`
	CommandActions   json.RawMessage `json:"commandActions,omitempty"`
}

func (*CommandExecution) isThreadItem()    {}
func (*CommandExecution) ItemType() string { return "commandExecution" }

// FileChange describes file edits performed by the agent. Wire discriminator:
// "fileChange". Schema (v2 codex 0.121.0) declares a Changes slice of parts —
// one per file touched. Prior to feature 187 this struct used a flat
// Path/Operation/Diff shape that was wrong against the real wire format.
type FileChange struct {
	ID      string           `json:"id,omitempty"`
	Status  string           `json:"status,omitempty"`
	Changes []FileChangePart `json:"changes,omitempty"`
}

// FileChangePart is one file's change within a FileChange item.
type FileChangePart struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // "create" | "modify" | "delete"
	Diff      string `json:"diff,omitempty"`
}

func (*FileChange) isThreadItem()    {}
func (*FileChange) ItemType() string { return "fileChange" }

// MCPToolCall captures a call the agent made to a Model Context Protocol
// tool. Wire discriminator: "mcpToolCall". Shape inferred — not yet seen
// in a captured transcript.
type MCPToolCall struct {
	ID         string                `json:"id,omitempty"`
	ServerName string                `json:"server,omitempty"`
	ToolName   string                `json:"tool,omitempty"`
	Input      json.RawMessage       `json:"arguments,omitempty"`
	Result     json.RawMessage       `json:"result,omitempty"`
	Status     string                `json:"status,omitempty"`
	Error      MCPToolCallErrorField `json:"error,omitempty"`
	DurationMs *int64                `json:"durationMs,omitempty"`
}

func (*MCPToolCall) isThreadItem()    {}
func (*MCPToolCall) ItemType() string { return "mcpToolCall" }

// ErrorText returns the MCP tool-call error message, or the empty string when
// the call did not fail. Preserves the pre-187 string-accessor contract while
// the underlying field now absorbs codex 0.121.0's object shape via
// MCPToolCallErrorField.UnmarshalJSON.
func (m *MCPToolCall) ErrorText() string { return m.Error.Message }

// WebSearch records a search the agent performed. Wire discriminator:
// "webSearch". Schema (v2) declares an `action` raw-JSON field; there is no
// `results` field in the real wire format. Prior to feature 187 this struct
// fabricated a Results []WebSearchResult field.
type WebSearch struct {
	ID     string          `json:"id,omitempty"`
	Query  string          `json:"query"`
	Action json.RawMessage `json:"action,omitempty"`
}

func (*WebSearch) isThreadItem()    {}
func (*WebSearch) ItemType() string { return "webSearch" }

// MemoryRead captures a lookup against the knowledge store. Shape inferred.
type MemoryRead struct {
	ID     string `json:"id,omitempty"`
	Query  string `json:"query"`
	Result string `json:"result,omitempty"`
}

func (*MemoryRead) isThreadItem()    {}
func (*MemoryRead) ItemType() string { return "memoryRead" }

// MemoryWrite captures a write to the knowledge store. Shape inferred.
type MemoryWrite struct {
	ID    string `json:"id,omitempty"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (*MemoryWrite) isThreadItem()    {}
func (*MemoryWrite) ItemType() string { return "memoryWrite" }

// Plan is a structured plan the agent produced. Shape inferred.
type Plan struct {
	ID     string `json:"id,omitempty"`
	Text   string `json:"text"`
	Status string `json:"status,omitempty"`
}

func (*Plan) isThreadItem()    {}
func (*Plan) ItemType() string { return "plan" }

// Reasoning is extended thinking output.
//
// Summary and Content are preserved as raw JSON elements because the codex
// wire format uses variable shapes (empty arrays during streaming,
// structured parts when completed). Callers interested in reasoning text
// should iterate these arrays or use the reasoning_delta events which
// carry flat text chunks.
type Reasoning struct {
	ID      string            `json:"id,omitempty"`
	Summary []json.RawMessage `json:"summary,omitempty"`
	Content []json.RawMessage `json:"content,omitempty"`
}

func (*Reasoning) isThreadItem()    {}
func (*Reasoning) ItemType() string { return "reasoning" }

// SystemError is emitted when the server wants to surface a non-turn-
// terminating error as an item (e.g., model validation error mid-turn).
// Wire discriminator: "systemError". Shape inferred.
type SystemError struct {
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

func (*SystemError) isThreadItem()    {}
func (*SystemError) ItemType() string { return "systemError" }

// HookPrompt is a hook-generated prompt fragment set. Wire discriminator:
// "hookPrompt". Fragments carries raw JSON entries the hook emitted — their
// shape varies per hook and is therefore preserved as RawMessage.
type HookPrompt struct {
	ID        string            `json:"id,omitempty"`
	Fragments []json.RawMessage `json:"fragments,omitempty"`
}

func (*HookPrompt) isThreadItem()    {}
func (*HookPrompt) ItemType() string { return "hookPrompt" }

// DynamicToolCall is a codex 0.121.0 parallel-call tool invocation opt-in.
// Wire discriminator: "dynamicToolCall". Used for non-MCP, non-builtin tool
// invocations where the tool name is resolved dynamically.
type DynamicToolCall struct {
	ID           string          `json:"id,omitempty"`
	Tool         string          `json:"tool,omitempty"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	Status       string          `json:"status,omitempty"`
	Success      *bool           `json:"success,omitempty"`
	ContentItems json.RawMessage `json:"contentItems,omitempty"`
	DurationMs   *int64          `json:"durationMs,omitempty"`
}

func (*DynamicToolCall) isThreadItem()    {}
func (*DynamicToolCall) ItemType() string { return "dynamicToolCall" }

// CollabAgentToolCall is a multi-agent delegation item. Wire discriminator:
// "collabAgentToolCall". US4's translator emits one MsgTask per AgentsStates
// entry so the PWA agent panel gets one row per delegated sub-agent.
type CollabAgentToolCall struct {
	ID                string       `json:"id,omitempty"`
	Tool              string       `json:"tool,omitempty"`
	Status            string       `json:"status,omitempty"`
	AgentsStates      []AgentState `json:"agentsStates,omitempty"`
	Model             string       `json:"model,omitempty"`
	Prompt            string       `json:"prompt,omitempty"`
	ReasoningEffort   string       `json:"reasoningEffort,omitempty"`
	SenderThreadID    string       `json:"senderThreadId,omitempty"`
	ReceiverThreadIDs []string     `json:"receiverThreadIds,omitempty"`
}

func (*CollabAgentToolCall) isThreadItem()    {}
func (*CollabAgentToolCall) ItemType() string { return "collabAgentToolCall" }

// AgentState is one sub-agent's state inside a CollabAgentToolCall.AgentsStates
// slice. Shape not yet firmed in the v2 schema — store as raw JSON until codex
// stabilizes the fields.
type AgentState struct {
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON captures the raw JSON payload verbatim so downstream callers
// can inspect it as the schema evolves.
func (a *AgentState) UnmarshalJSON(data []byte) error {
	cp := make(json.RawMessage, len(data))
	copy(cp, data)
	a.Raw = cp
	return nil
}

// ImageView describes an inline image the agent displayed. Wire discriminator:
// "imageView".
type ImageView struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path"`
}

func (*ImageView) isThreadItem()    {}
func (*ImageView) ItemType() string { return "imageView" }

// ImageGeneration describes an image the agent generated (DALL-E style). Wire
// discriminator: "imageGeneration".
type ImageGeneration struct {
	ID            string          `json:"id,omitempty"`
	Status        string          `json:"status,omitempty"`
	SavedPath     string          `json:"savedPath,omitempty"`
	RevisedPrompt string          `json:"revisedPrompt,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
}

func (*ImageGeneration) isThreadItem()    {}
func (*ImageGeneration) ItemType() string { return "imageGeneration" }

// EnteredReviewMode signals the agent entered a review flow. Wire discriminator:
// "enteredReviewMode". Review carries arbitrary JSON (schema is loose).
type EnteredReviewMode struct {
	ID     string          `json:"id,omitempty"`
	Review json.RawMessage `json:"review,omitempty"`
}

func (*EnteredReviewMode) isThreadItem()    {}
func (*EnteredReviewMode) ItemType() string { return "enteredReviewMode" }

// ExitedReviewMode signals the agent exited a review flow. Wire discriminator:
// "exitedReviewMode".
type ExitedReviewMode struct {
	ID     string          `json:"id,omitempty"`
	Review json.RawMessage `json:"review,omitempty"`
}

func (*ExitedReviewMode) isThreadItem()    {}
func (*ExitedReviewMode) ItemType() string { return "exitedReviewMode" }

// ContextCompaction signals codex compacted the conversation context. Wire
// discriminator: "contextCompaction". US4's translator maps it to MsgCompact.
type ContextCompaction struct {
	ID string `json:"id,omitempty"`
}

func (*ContextCompaction) isThreadItem()    {}
func (*ContextCompaction) ItemType() string { return "contextCompaction" }

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
// Populated from the flat "delta" string on item/agentMessage/delta.
type AgentMessageDelta struct {
	TextChunk string `json:"text_chunk"`
}

func (*AgentMessageDelta) isItemDelta()      {}
func (*AgentMessageDelta) DeltaType() string { return "agentMessage/delta" }

// ReasoningTextDelta is a streaming chunk of extended-thinking body text.
// Emitted on item/reasoning/textDelta.
type ReasoningTextDelta struct {
	TextChunk    string `json:"text_chunk"`
	ContentIndex int    `json:"content_index"`
}

func (*ReasoningTextDelta) isItemDelta()      {}
func (*ReasoningTextDelta) DeltaType() string { return "reasoning/textDelta" }

// ReasoningSummaryTextDelta is a streaming chunk of the reasoning summary.
// Emitted on item/reasoning/summaryTextDelta.
type ReasoningSummaryTextDelta struct {
	SummaryChunk string `json:"summary_chunk"`
	SummaryIndex int    `json:"summary_index"`
}

func (*ReasoningSummaryTextDelta) isItemDelta()      {}
func (*ReasoningSummaryTextDelta) DeltaType() string { return "reasoning/summaryTextDelta" }

// ReasoningSummaryPartAdded signals that a new summary part started.
// Emitted on item/reasoning/summaryPartAdded. Carries no text payload —
// follow-up textDelta events fill the part.
type ReasoningSummaryPartAdded struct {
	SummaryIndex int `json:"summary_index"`
}

func (*ReasoningSummaryPartAdded) isItemDelta()      {}
func (*ReasoningSummaryPartAdded) DeltaType() string { return "reasoning/summaryPartAdded" }

// CommandOutputDelta is a streaming chunk of a commandExecution's
// aggregated output. Wire shape is the flat "delta" string on
// item/commandExecution/outputDelta — codex does not split stdout and
// stderr at the SDK layer.
type CommandOutputDelta struct {
	OutputChunk string `json:"output_chunk"`
}

func (*CommandOutputDelta) isItemDelta()      {}
func (*CommandOutputDelta) DeltaType() string { return "commandExecution/outputDelta" }

// FileChangeOutputDelta is a streaming chunk of a fileChange diff.
// Emitted on item/fileChange/outputDelta.
type FileChangeOutputDelta struct {
	DiffChunk string `json:"diff_chunk"`
}

func (*FileChangeOutputDelta) isItemDelta()      {}
func (*FileChangeOutputDelta) DeltaType() string { return "fileChange/outputDelta" }

// PlanDelta is a streaming chunk of a plan item's body.
// Emitted on item/plan/delta.
type PlanDelta struct {
	Chunk string `json:"chunk"`
}

func (*PlanDelta) isItemDelta()      {}
func (*PlanDelta) DeltaType() string { return "plan/delta" }

// MCPToolCallProgress is a status update emitted during a long-running MCP
// tool call. Wire message is the flat "message" string on
// item/mcpToolCall/progress.
type MCPToolCallProgress struct {
	Message string `json:"message"`
}

func (*MCPToolCallProgress) isItemDelta()      {}
func (*MCPToolCallProgress) DeltaType() string { return "mcpToolCall/progress" }

// TerminalInteraction carries stdin sent to a running command's tty.
// Emitted on item/commandExecution/terminalInteraction.
type TerminalInteraction struct {
	ProcessID string `json:"process_id"`
	Stdin     string `json:"stdin"`
}

func (*TerminalInteraction) isItemDelta()      {}
func (*TerminalInteraction) DeltaType() string { return "commandExecution/terminalInteraction" }

// UnknownDelta is the forward-compat hatch for unrecognized delta types.
type UnknownDelta struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

func (*UnknownDelta) isItemDelta()        {}
func (u *UnknownDelta) DeltaType() string { return u.Type }
