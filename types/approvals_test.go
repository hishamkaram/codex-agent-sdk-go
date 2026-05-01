package types

import (
	"context"
	"testing"
)

func TestApprovalMethod_EveryKnownRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		req  ApprovalRequest
		want string
	}{
		{&CommandExecutionApprovalRequest{}, "item/commandExecution/requestApproval"},
		{&FileChangeApprovalRequest{}, "item/fileChange/requestApproval"},
		{&PermissionsApprovalRequest{}, "item/permissions/requestApproval"},
		{&ElicitationRequest{}, "mcpServer/elicitation/request"},
		{&ToolRequestUserInputRequest{}, "item/tool/requestUserInput"},
		{&UnknownApprovalRequest{Method: "future/req"}, "future/req"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.req.ApprovalMethod(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDefaultDenyApprovalCallback(t *testing.T) {
	t.Parallel()
	got := DefaultDenyApprovalCallback(context.Background(), &CommandExecutionApprovalRequest{Command: "x"})
	d, ok := got.(ApprovalDeny)
	if !ok {
		t.Fatalf("got %T, want ApprovalDeny", got)
	}
	if d.Reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestDefaultDenyApprovalCallback_RequestUserInput(t *testing.T) {
	t.Parallel()
	got := DefaultDenyApprovalCallback(context.Background(), &ToolRequestUserInputRequest{
		Questions: []ToolRequestUserInputQuestion{{ID: "approve"}},
	})
	resp, ok := got.(ToolRequestUserInputResponse)
	if !ok {
		t.Fatalf("got %T, want ToolRequestUserInputResponse", got)
	}
	answer, ok := resp.Answers["approve"]
	if !ok {
		t.Fatalf("missing answer for approve: %+v", resp.Answers)
	}
	if answer.Answers == nil {
		t.Fatal("answer slice must be empty, not nil")
	}
	if len(answer.Answers) != 0 {
		t.Fatalf("len(answer.Answers) = %d, want 0", len(answer.Answers))
	}
}

// Sanity: all four decision types satisfy the interface — caught at compile
// time, but a runtime check guards against someone breaking the sealing.
func TestApprovalDecisions_ImplementInterface(t *testing.T) {
	t.Parallel()
	var _ ApprovalDecision = ApprovalAccept{}
	var _ ApprovalDecision = ApprovalAcceptForSession{}
	var _ ApprovalDecision = ApprovalDeny{Reason: "x"}
	var _ ApprovalDecision = ApprovalCancel{Reason: "x"}
	var _ ApprovalDecision = ToolRequestUserInputResponse{}
}
