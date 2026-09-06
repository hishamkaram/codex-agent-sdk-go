package codex

import (
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestRootApprovalRequiresIdentity(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"thread", "turn", "item"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			root := "/project"
			request := &types.FileChangeApprovalRequest{ThreadID: "thread", TurnID: "turn", ItemID: "item", GrantRoot: &root}
			switch field {
			case "thread":
				request.ThreadID = ""
			case "turn":
				request.TurnID = ""
			case "item":
				request.ItemID = ""
			}
			if (&Client{}).resolveFileApproval(request) {
				t.Fatal("root approval accepted without complete identity")
			}
		})
	}
}

func TestRootOnlyApprovalDoesNotTrustUnresolvedChanges(t *testing.T) {
	t.Parallel()
	root := "/project"
	request := &types.FileChangeApprovalRequest{
		ThreadID: "thread", TurnID: "turn", ItemID: "item", GrantRoot: &root,
		Changes: []types.FileChangePart{{Path: "unresolved.txt", Operation: "create"}},
	}
	if !(&Client{}).resolveFileApproval(request) {
		t.Fatal("valid root request rejected")
	}
	if len(request.Changes) != 0 {
		t.Fatal("root-only request exposed unresolved changes")
	}
}
