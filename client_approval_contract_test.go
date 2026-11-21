package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_ApprovalCallback_MethodSpecificReplies(t *testing.T) {
	t.Parallel()

	opts := types.NewCodexOptions().WithApprovalCallback(func(_ context.Context, req types.ApprovalRequest) types.ApprovalDecision {
		switch r := req.(type) {
		case *types.PermissionsApprovalRequest:
			if r.EnvironmentID == "remote" && r.ItemID == "i" {
				return types.PermissionsApprovalResponse{
					Scope: types.PermissionGrantSession,
					Permissions: types.GrantedPermissionProfile{
						FileSystem: &types.GrantedFileSystemPermissions{
							Entries: []types.FileSystemPermissionEntry{{
								Path:   types.FileSystemPermissionPath{Type: types.FileSystemPathLiteral, Path: "/work/approved"},
								Access: types.FileSystemPermissionRead,
							}},
						},
					},
				}
			}
		case *types.ElicitationRequest:
			if r.Mode == types.ElicitationForm && r.ServerName == "forms" && r.TurnID == nil {
				return types.ElicitationResponse{
					Action:  types.ElicitationAccept,
					Content: json.RawMessage(`{"selected":"brief","includeDetails":false}`),
					Meta:    json.RawMessage(`{"source":"test"}`),
				}
			}
		}
		return types.ApprovalDeny{}
	})
	_, server := setupMockClient(t, opts, nil)

	// The permission request asks for both network and filesystem access. The
	// callback selects a single read root, so the response must omit network
	// and the second root. The signed ID is outside float64's exact range.
	server.push(json.RawMessage(`{"id":-9007199254740993,"method":"item/permissions/requestApproval","params":{"threadId":"T","turnId":"U","itemId":"i","environmentId":"remote","cwd":"/work","startedAtMs":1782904297563,"reason":"read input","permissions":{"network":{"enabled":true},"fileSystem":{"entries":[{"path":{"type":"path","path":"/work/approved"},"access":"read"},{"path":{"type":"path","path":"/work/other"},"access":"write"}]}}}}`))
	assertApprovalReply(t, server, "-9007199254740993", `{"permissions":{"fileSystem":{"entries":[{"path":{"type":"path","path":"/work/approved"},"access":"read"}]}},"scope":"session"}`)

	server.push(json.RawMessage(`{"id":"mcp-form","method":"mcpServer/elicitation/request","params":{"threadId":"T","turnId":null,"serverName":"forms","environmentId":"remote","mode":"form","message":"Choose format","requestedSchema":{"type":"object","properties":{"selected":{"type":"string"},"includeDetails":{"type":"boolean"}}},"server_name":42,"prompt":{}}}`))
	assertApprovalReply(t, server, `"mcp-form"`, `{"action":"accept","content":{"selected":"brief","includeDetails":false},"_meta":{"source":"test"}}`)
}

func TestClient_ApprovalCallback_CanceledDecisionDeniesGrant(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, server := setupMockClient(t, types.NewCodexOptions().WithApprovalCallback(func(_ context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		cancel()
		enabled := true
		return types.PermissionsApprovalResponse{
			Scope:       types.PermissionGrantTurn,
			Permissions: types.GrantedPermissionProfile{Network: &types.GrantedNetworkPermissions{Enabled: &enabled}},
		}
	}), nil)
	runMockApproval(t, c, ctx, jsonrpc.ServerRequest{
		ID:     json.RawMessage("0"),
		Method: "item/permissions/requestApproval",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","itemId":"i","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}`),
	})
	assertApprovalReply(t, server, "0", `{"permissions":{},"scope":"turn"}`)
}

func TestClient_ApprovalCallback_DefaultReplies(t *testing.T) {
	t.Parallel()
	_, server := setupMockClient(t, types.NewCodexOptions(), nil)
	server.push(json.RawMessage(`{"id":1,"method":"item/permissions/requestApproval","params":{"threadId":"T","turnId":"U","itemId":"i","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}}`))
	assertApprovalReply(t, server, "1", `{"permissions":{},"scope":"turn"}`)
	server.push(json.RawMessage(`{"id":2,"method":"mcpServer/elicitation/request","params":{"threadId":"T","turnId":"U","serverName":"forms","mode":"url","message":"Connect account","url":"https://example.com/connect","elicitationId":"flow-1"}}`))
	assertApprovalReply(t, server, "2", `{"action":"decline"}`)
}

func TestClient_ApprovalCallback_ElicitationModeCannotChange(t *testing.T) {
	t.Parallel()
	gotMode := make(chan types.ElicitationMode, 1)
	_, server := setupMockClient(t, types.NewCodexOptions().WithApprovalCallback(func(_ context.Context, req types.ApprovalRequest) types.ApprovalDecision {
		if elicitation, ok := req.(*types.ElicitationRequest); ok {
			gotMode <- elicitation.Mode
			elicitation.Mode = types.ElicitationForm
		}
		return types.ElicitationResponse{
			Action:  types.ElicitationAccept,
			Content: json.RawMessage(`{"format":"brief"}`),
		}
	}), nil)
	server.push(json.RawMessage(`{"id":"unsupported","method":"mcpServer/elicitation/request","params":{"threadId":"T","serverName":"forms","mode":"openai/form","message":"More information","requestedSchema":{"type":"object","properties":{"format":{"type":"string"}}}}}`))
	assertApprovalReply(t, server, `"unsupported"`, `{"action":"decline"}`)
	select {
	case mode := <-gotMode:
		if mode != "openai/form" {
			t.Fatalf("callback received mode %q, want openai/form", mode)
		}
	default:
		t.Fatal("callback did not receive the unsupported mode")
	}
}

func TestClient_ApprovalCallback_CanceledElicitationDropsContent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, server := setupMockClient(t, types.NewCodexOptions().WithApprovalCallback(func(_ context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		cancel()
		return types.ElicitationResponse{
			Action:  types.ElicitationAccept,
			Content: json.RawMessage(`{"format":"brief"}`),
		}
	}), nil)
	runMockApproval(t, c, ctx, jsonrpc.ServerRequest{
		ID:     json.RawMessage(`"canceled"`),
		Method: "mcpServer/elicitation/request",
		Params: json.RawMessage(`{"threadId":"T","serverName":"forms","mode":"form","message":"Choose format","requestedSchema":{"type":"object","properties":{"format":{"type":"string"}}}}`),
	})
	assertApprovalReply(t, server, `"canceled"`, `{"action":"cancel"}`)
}

func assertApprovalReply(t *testing.T, server *mockCodexServer, id, result string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	select {
	case raw := <-server.approvalReplies:
		var reply struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &reply); err != nil {
			t.Fatal(err)
		}
		if string(reply.ID) != id || reply.Error != nil {
			t.Fatalf("reply correlation = %s, want %s with no error", raw, id)
		}
		var got, want any
		if err := json.Unmarshal(reply.Result, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(result), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reply result = %s, want %s", reply.Result, result)
		}
	case <-ctx.Done():
		t.Fatalf("server never received reply to %s", id)
	}
}

func runMockApproval(t *testing.T, c *Client, ctx context.Context, request jsonrpc.ServerRequest) {
	t.Helper()
	worker := c.session.StartApprovals(ctx, c.session.Demux())
	t.Cleanup(func() {
		worker.Cancel()
		worker.Wait()
	})
	worker.Enqueue(request)
}
