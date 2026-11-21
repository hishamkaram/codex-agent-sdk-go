package types

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	Command     string `json:"command"`
	Cwd         string `json:"cwd,omitempty"`
	Reason      string `json:"reason,omitempty"`
	ThreadID    string `json:"threadId"`
	TurnID      string `json:"turnId"`
	ItemID      string `json:"itemId"`
	StartedAtMS int64  `json:"startedAtMs"`
}

func (*CommandExecutionApprovalRequest) isApprovalRequest() {}
func (*CommandExecutionApprovalRequest) ApprovalMethod() string {
	return "item/commandExecution/requestApproval"
}

// FileChangeApprovalRequest asks the user to approve file changes or a write root.
// Emitted when the model wants to create/modify/delete a file that
// approval policy does not auto-approve.
//
// Wire method: "item/fileChange/requestApproval".
type FileChangeApprovalRequest struct {
	// Changes is the latest active file set resolved before the callback.
	// Path/Operation/Diff remain a compatibility projection for single-file requests.
	Changes []FileChangePart `json:"changes,omitempty"`
	// GrantRoot proposes session-wide writes under this root. The upstream field
	// is unstable; its presence is a permission request, not a guarantee of access.
	GrantRoot   *string `json:"grantRoot,omitempty"`
	Path        string  `json:"path"`
	Operation   string  `json:"operation"` // "create" | "modify" | "delete"
	Diff        string  `json:"diff,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	ThreadID    string  `json:"threadId"`
	TurnID      string  `json:"turnId"`
	ItemID      string  `json:"itemId"`
	StartedAtMS int64   `json:"startedAtMs"`
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
	Cwd           string          `json:"cwd"`
	Permissions   json.RawMessage `json:"permissions"`
	EnvironmentID string          `json:"environmentId,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	ThreadID      string          `json:"threadId"`
	TurnID        string          `json:"turnId"`
	ItemID        string          `json:"itemId"`
	StartedAtMS   int64           `json:"startedAtMs"`
	// Permission and Scope preserve compatibility with pre-schema payloads.
	Permission string `json:"permission,omitempty"`
	Scope      string `json:"scope,omitempty"`
	// RawParams keeps the complete provider request, including future fields.
	RawParams json.RawMessage `json:"-"`
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
	ThreadID string  `json:"threadId,omitempty"`
	TurnID   *string `json:"turnId,omitempty"`
	// Keep the legacy marshal key. UnmarshalJSON prefers serverName on input.
	ServerName      string          `json:"server_name,omitempty"`
	EnvironmentID   *string         `json:"environmentId,omitempty"`
	Mode            ElicitationMode `json:"mode,omitempty"`
	Message         string          `json:"message,omitempty"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
	URL             string          `json:"url,omitempty"`
	ElicitationID   string          `json:"elicitationId,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
	// Prompt and Schema are compatibility projections of Message and
	// RequestedSchema when supplied by the provider.
	Prompt string          `json:"prompt"`
	Schema json.RawMessage `json:"schema,omitempty"`
	// RawParams retains the complete request, including unrecognized modes.
	RawParams json.RawMessage `json:"-"`
}

// ElicitationMode identifies a mode in an MCP request. Other observed values
// are available for inspection through ElicitationRequest.RawParams.
type ElicitationMode string

const (
	ElicitationForm ElicitationMode = "form"
	ElicitationURL  ElicitationMode = "url"
)

// UnmarshalJSON projects legacy request fields while keeping the current
// wire fields authoritative, including an explicitly empty message.
func (r *ElicitationRequest) UnmarshalJSON(data []byte) error {
	type fields ElicitationRequest
	var wire struct {
		fields
		LegacyServerName json.RawMessage `json:"server_name"`
		LegacyPrompt     json.RawMessage `json:"prompt"`
		ServerName       json.RawMessage `json:"serverName"`
		Message          json.RawMessage `json:"message"`
		Mode             json.RawMessage `json:"mode"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode elicitation request: %w", err)
	}
	req := ElicitationRequest(wire.fields)
	req.RawParams = bytes.Clone(data)
	name, err := decodeElicitationText(wire.ServerName, wire.LegacyServerName)
	if err != nil {
		return fmt.Errorf("decode elicitation server name: %w", err)
	}
	message, err := decodeElicitationText(wire.Message, wire.LegacyPrompt)
	if err != nil {
		return fmt.Errorf("decode elicitation message: %w", err)
	}
	req.ServerName = name
	req.Message = message
	req.Prompt = message
	if req.RequestedSchema != nil {
		req.Schema = bytes.Clone(req.RequestedSchema)
	} else {
		req.RequestedSchema = bytes.Clone(req.Schema)
	}
	if wire.Mode != nil {
		if modeErr := json.Unmarshal(wire.Mode, &req.Mode); modeErr != nil {
			return fmt.Errorf("decode elicitation mode: %w", modeErr)
		}
	} else if wire.ServerName == nil && wire.Message == nil && req.ServerName != "" &&
		wire.RequestedSchema == nil && req.Prompt != "" && req.Schema != nil {
		req.Mode = ElicitationForm
	}
	*r = req
	return nil
}

func decodeElicitationText(current, legacy json.RawMessage) (string, error) {
	if current == nil {
		current = legacy
	}
	if current == nil {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(current, &value); err != nil {
		return "", err
	}
	return value, nil
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

// ApprovalCancel asks the provider to abort a command or file approval turn.
// For MCP elicitation it cancels the elicitation. For a permission request it
// sends an empty grant.
type ApprovalCancel struct {
	Reason string `json:"reason,omitempty"`
}

func (ApprovalCancel) isApprovalDecision() {}

// PermissionGrantScope limits how long the provider can use a permission grant.
type PermissionGrantScope string

const (
	PermissionGrantTurn    PermissionGrantScope = "turn"
	PermissionGrantSession PermissionGrantScope = "session"
)

// PermissionsApprovalResponse explicitly selects the granted permissions and
// their scope. An empty profile grants nothing. StrictAutoReview is optional
// and may be enabled only for turn-scoped grants.
type PermissionsApprovalResponse struct {
	Permissions      GrantedPermissionProfile `json:"permissions"`
	Scope            PermissionGrantScope     `json:"scope"`
	StrictAutoReview *bool                    `json:"strictAutoReview,omitempty"`
}

func (PermissionsApprovalResponse) isApprovalDecision() {}

// GrantedPermissionProfile contains only the permissions the caller selected.
type GrantedPermissionProfile struct {
	Network    *GrantedNetworkPermissions    `json:"network,omitempty"`
	FileSystem *GrantedFileSystemPermissions `json:"fileSystem,omitempty"`
}

// GrantedNetworkPermissions controls the requested network grant.
// A nil Enabled leaves the network grant unspecified.
type GrantedNetworkPermissions struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// GrantedFileSystemPermissions selects filesystem access. Entries, when set,
// supersedes the legacy Read and Write root lists.
type GrantedFileSystemPermissions struct {
	Entries          []FileSystemPermissionEntry `json:"entries,omitzero"`
	GlobScanMaxDepth *uint                       `json:"globScanMaxDepth,omitempty"`
	Read             []string                    `json:"read,omitempty"`
	Write            []string                    `json:"write,omitempty"`
}

// FileSystemPermissionAccess selects access for one filesystem entry.
type FileSystemPermissionAccess string

const (
	FileSystemPermissionRead  FileSystemPermissionAccess = "read"
	FileSystemPermissionWrite FileSystemPermissionAccess = "write"
	FileSystemPermissionDeny  FileSystemPermissionAccess = "deny"
)

// FileSystemPermissionEntry associates an explicit path selection with access.
type FileSystemPermissionEntry struct {
	Path   FileSystemPermissionPath   `json:"path"`
	Access FileSystemPermissionAccess `json:"access"`
}

// FileSystemPermissionPathType selects the path, pattern or special value field.
type FileSystemPermissionPathType string

const (
	FileSystemPathLiteral FileSystemPermissionPathType = "path"
	FileSystemPathGlob    FileSystemPermissionPathType = "glob_pattern"
	FileSystemPathSpecial FileSystemPermissionPathType = "special"
)

// FileSystemPermissionPath is the tagged app-server path selection.
type FileSystemPermissionPath struct {
	Type    FileSystemPermissionPathType `json:"type"`
	Path    string                       `json:"path,omitempty"`
	Pattern string                       `json:"pattern,omitempty"`
	Value   *FileSystemPermissionSpecial `json:"value,omitempty"`
}

// FileSystemPermissionSpecial selects a provider-defined special root.
type FileSystemPermissionSpecial struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Subpath string `json:"subpath,omitempty"`
}

// ElicitationAction selects the response to an MCP elicitation request.
type ElicitationAction string

const (
	ElicitationAccept  ElicitationAction = "accept"
	ElicitationDecline ElicitationAction = "decline"
	ElicitationCancel  ElicitationAction = "cancel"
)

// ElicitationResponse carries an MCP response. Content is optional JSON for
// accept only. Meta is optional JSON written under the protocol's "_meta" key.
type ElicitationResponse struct {
	Action  ElicitationAction `json:"action"`
	Content json.RawMessage   `json:"content,omitempty"`
	Meta    json.RawMessage   `json:"_meta,omitempty"`
}

func (ElicitationResponse) isApprovalDecision() {}

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
// One Client invokes callbacks serially. Honor ctx.Done and return promptly
// when canceled. Use the supplied context for SDK calls so reentry protection
// can reject work that would wait for this Client's approval worker.
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
