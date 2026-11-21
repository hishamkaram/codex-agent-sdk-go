package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_ApprovalBackpressureClosesUndeliverableReply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		overflow bool
	}{
		{name: "callback"},
		{name: "overflow", overflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testRequiredApprovalReply(t, tc.overflow)
		})
	}
}

func testRequiredApprovalReply(t *testing.T, overflow bool) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 9*time.Second)
	defer stop()
	entered := make(chan struct{})
	opts := types.NewCodexOptions().WithApprovalCallback(func(ctx context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		close(entered)
		if overflow {
			<-ctx.Done()
		}
		return types.ApprovalDeny{}
	})
	peer := newMockCodexServer()
	writer := newBlockingFrameWriter()
	logger := sdklog.NewLoggerFromZap(nil)
	closed := make(chan error, 1)
	demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(peer.serverIn), jsonrpc.NewLineWriter(writer), logger,
		jsonrpc.WithOrderedServerMessages(),
		jsonrpc.WithUnrecoverableHandler(func(err error) { closed <- err }))
	client, err := NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if startErr := client.session.StartDispatcher(ctx, stop, demux, client.session.Dispatch); startErr != nil {
		t.Fatal(startErr)
	}
	demux.Run(ctx)
	defer func() {
		_ = client.Close(ctx)
		peer.close()
	}()
	ordered, err := client.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan error, 1)
	go func() {
		_, holdErr := demux.Send(ctx, "hold/writer", nil)
		held <- holdErr
	}()
	awaitExecutionTest(t, ctx, writer.writeEntered)
	send := func(id int) {
		peer.push(json.RawMessage(fmt.Sprintf(`{"id":%d,"method":"item/permissions/requestApproval","params":{"threadId":"held","turnId":"hold","itemId":"item","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}}`, id)))
	}
	send(1)
	awaitExecutionTest(t, ctx, entered)
	if overflow {
		// One callback is active and sixteen requests fill its queue.
		for id := 2; id <= 18; id++ {
			send(id)
		}
	}
	// Both the callback and overflow paths can queue a reply behind a
	// held writer slot without blocking a sibling's ordered terminal.
	peer.push(notify("turn/completed", map[string]string{"threadId": "sibling", "turnId": "other", "status": "completed"}))
	event := awaitExecutionTest(t, ctx, ordered)
	terminal, ok := event.Event.(*types.TurnCompleted)
	if !ok || terminal.TurnID != "other" || event.Err != nil {
		t.Fatalf("sibling terminal = %#v", event)
	}
	failure := awaitExecutionTest(t, ctx, closed)
	if !errors.Is(failure, context.DeadlineExceeded) && !errors.Is(failure, jsonrpc.ErrFramePoisoned) {
		t.Fatalf("connection fault = %v", failure)
	}
	awaitExecutionTest(t, ctx, held)
	if _, sendErr := demux.Send(ctx, "after/undeliverable", nil); !errors.Is(sendErr, jsonrpc.ErrClosed) {
		t.Fatalf("connection survived an undelivered required reply: %v", sendErr)
	}
}

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

func TestFileApprovalOrderedDispatch(t *testing.T) {
	t.Parallel()
	started := `{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"type":"fileChange","id":"item","changes":[{"path":"proof.txt","kind":{"type":"add"}}]}}}`
	for _, tc := range []struct {
		name, before, turn string
		wantCalls          int
	}{
		{"ordered", started, "turn", 1},
		{"missing", "", "turn", 0},
		{"wrong turn", started, "other", 0},
		{"completed", started + "\n" + `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn","status":"completed"}}}`, "turn", 0},
		{"decode gap", started + "\n" + `{broken`, "turn", 0},
		{"missing thread identity", started + "\n" + `{"method":"turn/completed","params":{"turn":{"id":"turn","status":"completed"}}}`, "turn", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request := `{"id":42,"method":"item/fileChange/requestApproval","params":{"threadId":"thread","turnId":"` + tc.turn + `","itemId":"item"}}`
			var calls atomic.Int32
			opts := types.NewCodexOptions()
			opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
				calls.Add(1)
				file := request.(*types.FileChangeApprovalRequest)
				if len(file.Changes) != 1 || file.Changes[0].Path != "proof.txt" {
					t.Errorf("wrong context: %+v", file)
				}
				return types.ApprovalAccept{}
			}
			_, server := setupMockClient(t, opts, nil)
			if _, err := server.serverOut.Write([]byte(tc.before + "\n" + request + "\n")); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Result struct {
					Decision string `json:"decision"`
				} `json:"result"`
			}
			select {
			case raw := <-server.approvalReplies:
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("server never received its reply")
			}
			if calls.Load() != int32(tc.wantCalls) {
				t.Fatalf("callback calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
			want := "decline"
			if tc.wantCalls == 1 {
				want = "accept"
			}
			if response.Result.Decision != want {
				t.Fatalf("decision = %q, want %q", response.Result.Decision, want)
			}
		})
	}
}

func TestFileApprovalExposesGrantRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, root  string
		files       bool
		invalidated bool
		wantCalls   int
	}{
		{"root only", `"/project"`, false, false, 1},
		{"root and files", `"/project"`, true, false, 1},
		{"blank root", `"   "`, true, false, 0},
		{"null root without files", `null`, false, false, 0},
		{"invalidated files cannot become root only", `"/project"`, true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			opts := types.NewCodexOptions()
			opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
				calls.Add(1)
				data, err := json.Marshal(request)
				if err != nil {
					t.Error(err)
					return types.ApprovalDeny{}
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Error(err)
					return types.ApprovalDeny{}
				}
				if string(fields["grantRoot"]) != tc.root {
					t.Errorf("grantRoot was not preserved: %s", data)
				}
				return types.ApprovalDeny{}
			}
			_, server := setupMockClient(t, opts, nil)
			if tc.files {
				startFileApprovalForUpdate(server)
			}
			if tc.invalidated {
				server.push(json.RawMessage(`{"method":"item/fileChange/patchUpdated","params":{"threadId":"thread","turnId":"turn","itemId":"item","changes":{}}}`))
			}
			server.push(json.RawMessage(`{"id":42,"method":"item/fileChange/requestApproval","params":{"threadId":"thread","turnId":"turn","itemId":"item","grantRoot":` + tc.root + `}}`))
			wantReply := `{"decision":"decline"}`
			if tc.wantCalls == 0 {
				wantReply = `{"decision":"decline","reason":"no approval callback configured"}`
			}
			assertApprovalReply(t, server, "42", wantReply)
			if got := int(calls.Load()); got != tc.wantCalls {
				t.Fatalf("callback calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

func startFileApprovalForUpdate(server *mockCodexServer) {
	server.push(json.RawMessage(`{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"type":"fileChange","id":"item","changes":[{"path":"old.txt","kind":{"type":"add"},"diff":"+old"}]}}}`))
}

func TestFileApprovalResolvesStartedItem(t *testing.T) {
	t.Parallel()
	captured := make(chan types.ApprovalRequest, 1)
	opts := types.NewCodexOptions()
	opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
		captured <- request
		return types.ApprovalDeny{}
	}
	_, server := setupMockClient(t, opts, nil)
	server.push(json.RawMessage(`{"method":"item/started","params":{"threadId":"thread-1","turnId":"turn-1","item":{"type":"fileChange","id":"item-1","changes":[{"path":"one.txt","kind":{"type":"add"},"diff":"+one"},{"path":"two.txt","kind":{"type":"update","move_path":"three.txt"},"diff":"+two"}]}}}`))
	server.push(json.RawMessage(`{"id":42,"method":"item/fileChange/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}}`))
	assertApprovalReply(t, server, "42", `{"decision":"decline"}`)
	var request types.ApprovalRequest
	select {
	case request = <-captured:
	default:
		t.Fatal("file approval callback was not called")
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Changes []types.FileChangePart `json:"changes"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 2 || result.Changes[0].Path != "one.txt" || result.Changes[1].Path != "two.txt" {
		t.Fatalf("approval lost file context: %s", data)
	}
}
