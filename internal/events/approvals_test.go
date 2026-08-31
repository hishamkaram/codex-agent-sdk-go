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
		json.RawMessage(`{"cwd":"/work","permissions":{"network":{"enabled":true}},"reason":"network access","threadId":"thread-child","turnId":"turn-child","itemId":"item-child","startedAtMs":1782904297563}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	p := r.(*types.PermissionsApprovalRequest)
	if p.Cwd != "/work" || p.Reason != "network access" || p.ThreadID != "thread-child" || p.TurnID != "turn-child" || p.ItemID != "item-child" || p.StartedAtMS != 1782904297563 {
		t.Fatalf("%+v", p)
	}
	if string(p.Permissions) != `{"network":{"enabled":true}}` {
		t.Fatalf("Permissions = %s", p.Permissions)
	}
}

func TestParseApprovalRequest_Elicitation(t *testing.T) {
	t.Parallel()
	r, _ := ParseApprovalRequest(
		"mcpServer/elicitation/request",
		json.RawMessage(`{"server_name":"docs","prompt":"password?","schema":{"type":"string"}}`),
	)
	e := r.(*types.ElicitationRequest)
	if e.ServerName != "docs" || e.Prompt != "password?" || len(e.Schema) == 0 {
		t.Fatalf("%+v", e)
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
			m := EncodeApprovalDecision(tt.in)
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
