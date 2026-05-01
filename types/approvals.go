package types

import (
	"context"
	"encoding/json"
)

// ApprovalRequest is the interface implemented by every concrete kind of
// server-initiated approval prompt. The codex server sends these as
// JSON-RPC requests (with id, expecting a response). The SDK dispatches
// each one to the ApprovalCallback configured on CodexOptions and sends
// the callback's returned ApprovalDecision back as the response.
type ApprovalRequest interface {
	isApprovalRequest()
	// ApprovalMethod returns the JSON-RPC method name that produced this
	// request.
	ApprovalMethod() string
}

// CommandExecutionApprovalRequest asks the user to approve running a shell
// command. Emitted when the model wants to run a command that the
// configured ApprovalPolicy does not auto-approve.
//
// Wire method: "item/commandExecution/requestApproval".
type CommandExecutionApprovalRequest struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (*CommandExecutionApprovalRequest) isApprovalRequest() {}
func (*CommandExecutionApprovalRequest) ApprovalMethod() string {
	return "item/commandExecution/requestApproval"
}

// FileChangeApprovalRequest asks the user to approve a file modification.
// Emitted when the model wants to create/modify/delete a file that
// approval policy does not auto-approve.
//
// Wire method: "item/fileChange/requestApproval".
type FileChangeApprovalRequest struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // "create" | "modify" | "delete"
	Diff      string `json:"diff,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (*FileChangeApprovalRequest) isApprovalRequest() {}
func (*FileChangeApprovalRequest) ApprovalMethod() string {
	return "item/fileChange/requestApproval"
}

// PermissionsApprovalRequest asks the user to grant a broader permission
// (e.g., network egress, out-of-workspace access).
//
// Wire method: "item/permissions/requestApproval".
type PermissionsApprovalRequest struct {
	Permission string `json:"permission"`
	Scope      string `json:"scope,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (*PermissionsApprovalRequest) isApprovalRequest() {}
func (*PermissionsApprovalRequest) ApprovalMethod() string {
	return "item/permissions/requestApproval"
}

// ElicitationRequest asks the user to provide additional input (e.g., a
// clarification, a secret, a choice). Originates from an MCP server's
// "elicitation" flow rather than codex directly.
//
// Wire method: "mcpServer/elicitation/request".
type ElicitationRequest struct {
	ServerName string          `json:"server_name,omitempty"`
	Prompt     string          `json:"prompt"`
	Schema     json.RawMessage `json:"schema,omitempty"`
}

func (*ElicitationRequest) isApprovalRequest() {}
func (*ElicitationRequest) ApprovalMethod() string {
	return "mcpServer/elicitation/request"
}

// ToolRequestUserInputRequest asks the user to answer one or more structured
// questions from Codex's request_user_input tool.
//
// Wire method: "item/tool/requestUserInput".
type ToolRequestUserInputRequest struct {
	ItemID    string                         `json:"itemId"`
	Questions []ToolRequestUserInputQuestion `json:"questions"`
	ThreadID  string                         `json:"threadId"`
	TurnID    string                         `json:"turnId"`
}

func (*ToolRequestUserInputRequest) isApprovalRequest() {}
func (*ToolRequestUserInputRequest) ApprovalMethod() string {
	return "item/tool/requestUserInput"
}

// ToolRequestUserInputQuestion is one question in a Codex request_user_input
// prompt. Options is nil when Codex expects free-form text.
type ToolRequestUserInputQuestion struct {
	Header   string                       `json:"header"`
	ID       string                       `json:"id"`
	IsOther  bool                         `json:"isOther,omitempty"`
	IsSecret bool                         `json:"isSecret,omitempty"`
	Options  []ToolRequestUserInputOption `json:"options,omitempty"`
	Question string                       `json:"question"`
}

// ToolRequestUserInputOption is a selectable answer for a request_user_input
// question.
type ToolRequestUserInputOption struct {
	Description string `json:"description"`
	Label       string `json:"label"`
}

// UnknownApprovalRequest is the forward-compat hatch for approval methods
// the SDK doesn't recognize. The Method and Params fields carry the raw
// request. Callers should default-deny or default-decline unknown methods.
type UnknownApprovalRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (*UnknownApprovalRequest) isApprovalRequest()       {}
func (u *UnknownApprovalRequest) ApprovalMethod() string { return u.Method }

// ApprovalDecision is the response the caller returns from an
// ApprovalCallback. Exactly one concrete type should be returned. The SDK
// serializes the decision into a JSON-RPC response with the matching id.
type ApprovalDecision interface {
	isApprovalDecision()
}

// ApprovalAccept approves the request. The codex server proceeds with the
// action. Corresponds to wire decision "accept".
type ApprovalAccept struct{}

func (ApprovalAccept) isApprovalDecision() {}

// ApprovalAcceptForSession approves the request AND records an allow-rule
// for the current session so analogous future requests auto-approve.
// Corresponds to wire decision "acceptForSession".
type ApprovalAcceptForSession struct{}

func (ApprovalAcceptForSession) isApprovalDecision() {}

// ApprovalDeny rejects the request. The codex server aborts the specific
// action and may continue the turn with an alternative plan. Corresponds
// to wire decision "decline".
type ApprovalDeny struct {
	Reason string `json:"reason,omitempty"`
}

func (ApprovalDeny) isApprovalDecision() {}

// ApprovalCancel aborts the current turn entirely. Corresponds to wire
// decision "cancel".
type ApprovalCancel struct {
	Reason string `json:"reason,omitempty"`
}

func (ApprovalCancel) isApprovalDecision() {}

// ToolRequestUserInputResponse answers a Codex request_user_input prompt.
// The map keys are the original question ids from ToolRequestUserInputRequest.
type ToolRequestUserInputResponse struct {
	Answers map[string]ToolRequestUserInputAnswer `json:"answers"`
}

func (ToolRequestUserInputResponse) isApprovalDecision() {}

// ToolRequestUserInputAnswer carries one or more answer strings for a single
// request_user_input question.
type ToolRequestUserInputAnswer struct {
	Answers []string `json:"answers"`
}

// ApprovalCallback is the signature for the user-supplied approval handler.
// The callback MUST return promptly (a blocking approval stalls the turn)
// and MUST NOT panic. The ctx is the turn's context — if it fires Done,
// the callback should return ApprovalCancel.
type ApprovalCallback func(ctx context.Context, req ApprovalRequest) ApprovalDecision

// DefaultDenyApprovalCallback is a safe no-op approval handler that denies
// every request with a generic reason. Useful for tests and for clients
// that only want auto-approved actions.
func DefaultDenyApprovalCallback(ctx context.Context, req ApprovalRequest) ApprovalDecision {
	_ = ctx
	if r, ok := req.(*ToolRequestUserInputRequest); ok {
		answers := make(map[string]ToolRequestUserInputAnswer, len(r.Questions))
		for _, q := range r.Questions {
			answers[q.ID] = ToolRequestUserInputAnswer{Answers: []string{}}
		}
		return ToolRequestUserInputResponse{Answers: answers}
	}
	return ApprovalDeny{Reason: "no approval callback configured"}
}
