package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Feature 187 US2: CommandExecutionApprovalRequest.Cwd tag corrected from
// `working_directory` to `cwd` per v2 schema. Fixture-compensation per
// .claude/rules/fixture-compensation.md (test-only, minimum edit to keep
// FR-016 satisfied under new invariant).
func TestParseApprovalRequest_CommandExecution(t *testing.T) {
	t.Parallel()
	r, err := ParseApprovalRequest(
		"item/commandExecution/requestApproval",
		json.RawMessage(`{"command":"rm -rf /","cwd":"/tmp","reason":"destructive","threadId":"thread-1","turnId":"turn-1","itemId":"item-1","startedAtMs":1782904297561}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	c := r.(*types.CommandExecutionApprovalRequest)
	if c.Command != "rm -rf /" || c.Cwd != "/tmp" || c.Reason != "destructive" || c.ThreadID != "thread-1" || c.TurnID != "turn-1" || c.ItemID != "item-1" || c.StartedAtMS != 1782904297561 {
		t.Fatalf("%+v", c)
	}
	if r.ApprovalMethod() != "item/commandExecution/requestApproval" {
		t.Fatal("wrong method")
	}
}

func TestParseApprovalRequest_FileChange(t *testing.T) {
	t.Parallel()
	r, _ := ParseApprovalRequest(
		"item/fileChange/requestApproval",
		json.RawMessage(`{"path":"/a.go","operation":"delete","diff":"...","reason":"cleanup","threadId":"thread-2","turnId":"turn-2","itemId":"item-2","startedAtMs":1782904297562}`),
	)
	f := r.(*types.FileChangeApprovalRequest)
	if f.Path != "/a.go" || f.Operation != "delete" || f.ThreadID != "thread-2" || f.TurnID != "turn-2" || f.ItemID != "item-2" || f.StartedAtMS != 1782904297562 {
		t.Fatalf("%+v", f)
	}
}

func TestParseApprovalRequest_Permissions(t *testing.T) {
	t.Parallel()
	r, err := ParseApprovalRequest(
		"item/permissions/requestApproval",
		json.RawMessage(`{"cwd":"/work","permissions":{"network":{"enabled":true}},"environmentId":"remote","reason":"network access","threadId":"thread-child","turnId":"turn-child","itemId":"item-child","startedAtMs":1782904297563,"future":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	p := r.(*types.PermissionsApprovalRequest)
	if p.Cwd != "/work" || p.EnvironmentID != "remote" || p.Reason != "network access" || p.ThreadID != "thread-child" || p.TurnID != "turn-child" || p.ItemID != "item-child" || p.StartedAtMS != 1782904297563 {
		t.Fatalf("%+v", p)
	}
	if string(p.Permissions) != `{"network":{"enabled":true}}` {
		t.Fatalf("Permissions = %s", p.Permissions)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(p.RawParams, &raw); err != nil || string(raw["future"]) != "true" {
		t.Fatalf("future permission data was dropped: %s", p.RawParams)
	}
}

func TestParseApprovalRequest_Elicitation(t *testing.T) {
	t.Parallel()
	r, err := ParseApprovalRequest(
		"mcpServer/elicitation/request",
		json.RawMessage(`{"server_name":"docs","prompt":"password?","schema":{"type":"string"}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	e := r.(*types.ElicitationRequest)
	if e.ServerName != "docs" || e.Prompt != "password?" || e.Message != e.Prompt || string(e.RequestedSchema) != string(e.Schema) || e.Mode != types.ElicitationForm {
		t.Fatalf("%+v", e)
	}
}

func TestParseApprovalRequest_CurrentElicitationModes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		wire  string
		check func(*testing.T, *types.ElicitationRequest)
	}{
		{
			name: "form overrides legacy fields",
			wire: `{"threadId":"T","turnId":null,"serverName":"current","environmentId":"remote","mode":"form","message":"","requestedSchema":{"type":"object","properties":{"topic":{"type":"string"}}},"_meta":{"source":"form"},"server_name":"legacy","prompt":"old","schema":{"type":"number"}}`,
			check: func(t *testing.T, r *types.ElicitationRequest) {
				t.Helper()
				if r.ThreadID != "T" || r.TurnID != nil || r.ServerName != "current" || r.EnvironmentID == nil || *r.EnvironmentID != "remote" {
					t.Fatalf("form identity = %+v", r)
				}
				if r.Mode != types.ElicitationForm || r.Message != "" || r.Prompt != "" ||
					string(r.RequestedSchema) != `{"type":"object","properties":{"topic":{"type":"string"}}}` ||
					string(r.Schema) != string(r.RequestedSchema) || string(r.Meta) != `{"source":"form"}` {
					t.Fatalf("form data = %+v", r)
				}
			},
		},
		{
			name: "URL",
			wire: `{"threadId":"T","turnId":"U","serverName":"docs","mode":"url","message":"Connect account","url":"https://example.com/connect","elicitationId":"flow-1"}`,
			check: func(t *testing.T, r *types.ElicitationRequest) {
				t.Helper()
				if r.Mode != types.ElicitationURL || r.TurnID == nil || *r.TurnID != "U" || r.Prompt != "Connect account" ||
					r.URL != "https://example.com/connect" || r.ElicitationID != "flow-1" || r.RequestedSchema != nil {
					t.Fatalf("URL request = %+v", r)
				}
			},
		},
		{
			name: "future mode",
			wire: `{"threadId":"T","serverName":"docs","mode":"openai/userVerification","title":"verify","challenge":"opaque"}`,
			check: func(t *testing.T, r *types.ElicitationRequest) {
				t.Helper()
				if r.Mode != "openai/userVerification" {
					t.Fatalf("future mode = %q", r.Mode)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(tt.wire)
			req, err := ParseApprovalRequest("mcpServer/elicitation/request", raw)
			if err != nil {
				t.Fatal(err)
			}
			elicit := req.(*types.ElicitationRequest)
			tt.check(t, elicit)
			raw[0] = '['
			if string(elicit.RawParams) != tt.wire {
				t.Fatalf("raw request = %s", elicit.RawParams)
			}
		})
	}
}

func TestParseApprovalRequest_ToolRequestUserInput(t *testing.T) {
	t.Parallel()
	r, err := ParseApprovalRequest(
		"item/tool/requestUserInput",
		json.RawMessage(`{"itemId":"item-1","threadId":"thread-1","turnId":"turn-1","questions":[{"header":"Plan","id":"approve","question":"Proceed?","isOther":true,"options":[{"label":"Proceed","description":"Start implementation"}]}]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	qr, ok := r.(*types.ToolRequestUserInputRequest)
	if !ok {
		t.Fatalf("got %T", r)
	}
	if qr.ItemID != "item-1" || qr.ThreadID != "thread-1" || qr.TurnID != "turn-1" {
		t.Fatalf("%+v", qr)
	}
	if len(qr.Questions) != 1 {
		t.Fatalf("len(Questions) = %d", len(qr.Questions))
	}
	q := qr.Questions[0]
	if q.ID != "approve" || q.Header != "Plan" || q.Question != "Proceed?" || !q.IsOther {
		t.Fatalf("%+v", q)
	}
	if len(q.Options) != 1 || q.Options[0].Label != "Proceed" || q.Options[0].Description != "Start implementation" {
		t.Fatalf("%+v", q.Options)
	}
	if r.ApprovalMethod() != "item/tool/requestUserInput" {
		t.Fatal("wrong method")
	}
}

func TestParseApprovalRequest_UnknownMethodFallback(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"custom":"field"}`)
	r, _ := ParseApprovalRequest("some/new/approval", raw)
	u, ok := r.(*types.UnknownApprovalRequest)
	if !ok {
		t.Fatalf("got %T", r)
	}
	if u.Method != "some/new/approval" || u.ApprovalMethod() != "some/new/approval" {
		t.Fatal("method not preserved")
	}
	if string(u.Params) != string(raw) {
		t.Fatal("params not preserved")
	}
}

// TestParseApprovalRequest_CommandExecution_CwdTag pins US2-AC4 behavior:
// the v2 schema tags the working-directory field as `cwd` (not
// `working_directory` — the legacy tag). A real codex app-server that emits
// `cwd` would silently drop the field into an empty Go string under the old
// tag. This test asserts the new tag binds correctly.
func TestParseApprovalRequest_CommandExecution_CwdTag(t *testing.T) {
	t.Parallel()

	r, err := ParseApprovalRequest(
		"item/commandExecution/requestApproval",
		json.RawMessage(`{"command":"rm -rf foo","cwd":"/home/h","reason":"danger"}`),
	)
	if err != nil {
		t.Fatalf("ParseApprovalRequest returned error: %v", err)
	}
	c, ok := r.(*types.CommandExecutionApprovalRequest)
	if !ok {
		t.Fatalf("ParseApprovalRequest returned %T, want *types.CommandExecutionApprovalRequest", r)
	}
	if c.Command != "rm -rf foo" {
		t.Errorf("Command = %q, want %q", c.Command, "rm -rf foo")
	}
	if c.Cwd != "/home/h" {
		t.Errorf("Cwd = %q, want %q", c.Cwd, "/home/h")
	}
	if c.Reason != "danger" {
		t.Errorf("Reason = %q, want %q", c.Reason, "danger")
	}
}

func TestEncodeApprovalDecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   types.ApprovalDecision
		want string
	}{
		{"accept", types.ApprovalAccept{}, `{"decision":"accept"}`},
		{"acceptForSession", types.ApprovalAcceptForSession{}, `{"decision":"acceptForSession"}`},
		{"deny_no_reason", types.ApprovalDeny{}, `{"decision":"decline"}`},
		{"deny_with_reason", types.ApprovalDeny{Reason: "unsafe"}, `{"decision":"decline","reason":"unsafe"}`},
		{"cancel", types.ApprovalCancel{Reason: "user aborted"}, `{"decision":"cancel","reason":"user aborted"}`},
		{
			"user_input",
			types.ToolRequestUserInputResponse{
				Answers: map[string]types.ToolRequestUserInputAnswer{
					"approve": {Answers: []string{"Proceed"}},
				},
			},
			`{"answers":{"approve":{"answers":["Proceed"]}}}`,
		},
		{
			"user_input_empty",
			types.ToolRequestUserInputResponse{
				Answers: map[string]types.ToolRequestUserInputAnswer{
					"approve": {},
				},
			},
			`{"answers":{"approve":{"answers":[]}}}`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var req types.ApprovalRequest = &types.CommandExecutionApprovalRequest{}
			if _, ok := tt.in.(types.ToolRequestUserInputResponse); ok {
				req = &types.ToolRequestUserInputRequest{}
			}
			m, err := EncodeApprovalDecision(req, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Fatalf("got %q, want %q", b, tt.want)
			}
		})
	}
}

func TestEncodePermissionGrants(t *testing.T) {
	t.Parallel()
	enabled, disabled := true, false
	zeroDepth := uint(0)
	network := types.GrantedPermissionProfile{Network: &types.GrantedNetworkPermissions{Enabled: &enabled}}
	emptyGrant := `{"permissions":{},"scope":"turn"}`

	tests := []struct {
		name     string
		decision types.ApprovalDecision
		want     string
		invalid  bool
	}{
		{"denial", types.ApprovalDeny{Reason: "not approved"}, emptyGrant, false},
		{"no decision", nil, emptyGrant, false},
		{"unsupported accept", types.ApprovalAcceptForSession{}, emptyGrant, true},
		{"nil grant", (*types.PermissionsApprovalResponse)(nil), emptyGrant, true},
		{"invalid scope", types.PermissionsApprovalResponse{Permissions: network, Scope: "forever"}, emptyGrant, true},
		{"strict session", types.PermissionsApprovalResponse{
			Permissions: network, Scope: types.PermissionGrantSession, StrictAutoReview: &enabled,
		}, emptyGrant, true},
		{"network only", &types.PermissionsApprovalResponse{
			Permissions: network, Scope: types.PermissionGrantTurn, StrictAutoReview: &enabled,
		}, `{"permissions":{"network":{"enabled":true}},"scope":"turn","strictAutoReview":true}`, false},
		{"explicit false", types.PermissionsApprovalResponse{
			Permissions: types.GrantedPermissionProfile{Network: &types.GrantedNetworkPermissions{Enabled: &disabled}},
			Scope:       types.PermissionGrantSession, StrictAutoReview: &disabled,
		}, `{"permissions":{"network":{"enabled":false}},"scope":"session","strictAutoReview":false}`, false},
		{"Windows path", types.PermissionsApprovalResponse{
			Permissions: types.GrantedPermissionProfile{FileSystem: &types.GrantedFileSystemPermissions{Read: []string{`C:\work\selected`}}},
			Scope:       types.PermissionGrantTurn,
		}, `{"permissions":{"fileSystem":{"read":["C:\\work\\selected"]}},"scope":"turn"}`, false},
		{"explicit empty entries", types.PermissionsApprovalResponse{
			Permissions: types.GrantedPermissionProfile{FileSystem: &types.GrantedFileSystemPermissions{
				Entries: []types.FileSystemPermissionEntry{}, Write: []string{"/work/legacy"},
			}},
			Scope: types.PermissionGrantTurn,
		}, `{"permissions":{"fileSystem":{"entries":[],"write":["/work/legacy"]}},"scope":"turn"}`, false},
		{"relative legacy root", types.PermissionsApprovalResponse{
			Permissions: types.GrantedPermissionProfile{
				Network:    network.Network,
				FileSystem: &types.GrantedFileSystemPermissions{Write: []string{"work/relative"}},
			},
			Scope: types.PermissionGrantTurn,
		}, emptyGrant, true},
		{"relative literal entry", types.PermissionsApprovalResponse{
			Permissions: types.GrantedPermissionProfile{FileSystem: &types.GrantedFileSystemPermissions{
				Entries: []types.FileSystemPermissionEntry{{
					Access: types.FileSystemPermissionWrite,
					Path:   types.FileSystemPermissionPath{Type: types.FileSystemPathLiteral, Path: "D:relative"},
				}},
			}},
			Scope: types.PermissionGrantTurn,
		}, emptyGrant, true},
		{"invalid glob depth", types.PermissionsApprovalResponse{
			Permissions: types.GrantedPermissionProfile{FileSystem: &types.GrantedFileSystemPermissions{GlobScanMaxDepth: &zeroDepth}},
			Scope:       types.PermissionGrantTurn,
		}, emptyGrant, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkApprovalDecision(t, &types.PermissionsApprovalRequest{}, tt.decision, tt.want, tt.invalid)
		})
	}
}

func TestEncodeElicitationActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mode     types.ElicitationMode
		decision types.ApprovalDecision
		want     string
		invalid  bool
	}{
		{"deny", types.ElicitationForm, types.ApprovalDeny{}, `{"action":"decline"}`, false},
		{"cancel", types.ElicitationForm, types.ApprovalCancel{}, `{"action":"cancel"}`, false},
		{"wrong type", types.ElicitationForm, types.ApprovalAcceptForSession{}, `{"action":"decline"}`, true},
		{"typed nil", types.ElicitationForm, (*types.ElicitationResponse)(nil), `{"action":"decline"}`, true},
		{"bad action", types.ElicitationForm, types.ElicitationResponse{Action: "approve"}, `{"action":"decline"}`, true},
		{"bad JSON", types.ElicitationForm, types.ElicitationResponse{
			Action: types.ElicitationAccept, Content: json.RawMessage(`{"answer":`),
		}, `{"action":"decline"}`, true},
		{"bad metadata", types.ElicitationForm, types.ElicitationResponse{
			Action: types.ElicitationAccept, Meta: json.RawMessage(`{"source":`),
		}, `{"action":"decline"}`, true},
		{"future mode", "custom/verification", types.ElicitationResponse{Action: types.ElicitationAccept}, `{"action":"decline"}`, true},
		{"URL accept", types.ElicitationURL, &types.ElicitationResponse{Action: types.ElicitationAccept}, `{"action":"accept"}`, false},
		{"explicit null", types.ElicitationForm, types.ElicitationResponse{
			Action: types.ElicitationAccept, Content: json.RawMessage("null"), Meta: json.RawMessage("null"),
		}, `{"action":"accept","content":null,"_meta":null}`, false},
		{"cancel drops content", types.ElicitationURL, types.ElicitationResponse{
			Action: types.ElicitationCancel, Content: json.RawMessage(`{"selected":true}`),
		}, `{"action":"cancel"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkApprovalDecision(t, &types.ElicitationRequest{Mode: tt.mode}, tt.decision, tt.want, tt.invalid)
		})
	}
}

func checkApprovalDecision(t *testing.T, req types.ApprovalRequest, decision types.ApprovalDecision, want string, invalid bool) {
	t.Helper()
	got, err := EncodeApprovalDecision(req, decision)
	if (err != nil) != invalid {
		t.Fatalf("encode decision error = %v, expected invalid = %t", err, invalid)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}
