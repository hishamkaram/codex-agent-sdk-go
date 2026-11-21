package codex

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_ThreadConfigurationWireScope(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	cwd, root := filepath.Join(base, "work"), filepath.Join(base, "sibling")
	sourceReply := workspaceThreadReply(t, "source", cwd, false, []string{root})
	forkReply := workspaceThreadReply(t, "fork", cwd, true, []string{})

	defaults := types.NewCodexOptions().WithSandbox(types.SandboxWorkspaceWrite).
		WithWebSearchMode(types.WebSearchCached).WithReasoningEffort("medium").
		WithMCPServers(map[string]types.McpServerConfig{
			"default": types.McpStdioConfig{Command: "default-mcp"},
		})
	startCalls := 0
	c, server := setupMockClient(t, defaults, func(req jsonrpc.Request) jsonrpc.Response {
		var result json.RawMessage
		switch req.Method {
		case "thread/start":
			startCalls++
			if startCalls == 1 {
				result = sourceReply
			} else {
				result = json.RawMessage(`{"thread":{"id":"second"}}`)
			}
		case "thread/fork":
			result = forkReply
		case "thread/resume":
			result = sourceReply
		default:
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
		}
		return jsonrpc.Response{ID: req.ID, Result: result}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	offline, online := false, true
	source, err := c.StartThread(ctx, &types.ThreadOptions{
		Cwd: cwd, Model: "configured-model", ApprovalPolicy: types.ApprovalNever,
		WebSearchMode: types.WebSearchDisabled, ReasoningEffort: "high", ThreadSource: "interactive",
		NetworkAccessEnabled: &offline, AdditionalDirectories: []string{root, root},
		MCPServers: map[string]types.McpServerConfig{
			"local": types.McpStdioConfig{Command: "local-mcp"},
		},
	})
	if err != nil || source.Cwd() != cwd {
		t.Fatalf("start: thread=%v error=%v", source, err)
	}
	if _, startErr := c.StartThread(ctx, nil); startErr != nil {
		t.Fatal(startErr)
	}
	branch, _, err := c.ForkThread(ctx, source.ID(), &types.ThreadOptions{
		Cwd: cwd, WebSearchMode: types.WebSearchIndexed, ThreadSource: "scheduled:daily",
		NetworkAccessEnabled: &online, AdditionalDirectories: []string{},
	})
	if err != nil || branch.Cwd() != cwd {
		t.Fatalf("fork: thread=%v error=%v", branch, err)
	}
	if _, err := c.ResumeThread(ctx, source.ID(), &types.ResumeOptions{
		WebSearchMode: types.WebSearchLive, ReasoningEffort: "low",
	}); err != nil {
		t.Fatal(err)
	}

	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "thread/start", "thread/start", "thread/fork", "thread/resume")
	assertThreadConfigJSON(t, wire[0].Params, string(threadConfigFixture(t, map[string]any{
		"cwd": cwd, "model": "configured-model", "sandbox": "workspace-write", "approvalPolicy": "never", "threadSource": "interactive",
		"config": map[string]any{
			"web_search": "disabled", "model_reasoning_effort": "high",
			"mcp_servers":             map[string]any{"local": map[string]string{"command": "local-mcp"}},
			"sandbox_workspace_write": map[string]any{"network_access": false, "writable_roots": []string{root}},
		},
	})))
	assertThreadConfigJSON(t, wire[1].Params, `{
		"sandbox":"workspace-write","approvalPolicy":"on-request",
		"config":{"web_search":"cached","model_reasoning_effort":"medium","mcp_servers":{"default":{"command":"default-mcp"}}}
	}`)
	assertThreadConfigJSON(t, wire[2].Params, string(threadConfigFixture(t, map[string]any{
		"threadId": "source", "cwd": cwd, "sandbox": "workspace-write", "approvalPolicy": "on-request", "threadSource": "scheduled:daily",
		"config": map[string]any{
			"web_search": "indexed", "model_reasoning_effort": "medium",
			"mcp_servers":             map[string]any{"default": map[string]string{"command": "default-mcp"}},
			"sandbox_workspace_write": map[string]any{"network_access": true, "writable_roots": []string{}},
		},
	})))
	assertThreadConfigJSON(t, wire[3].Params, `{"threadId":"source","config":{"web_search":"live","model_reasoning_effort":"low"}}`)
}

func TestClient_ThreadPolicyRejectsBeforeRegistration(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	cwd, root := filepath.Join(base, "work"), filepath.Join(base, "sibling")
	unconfirmedStart := workspaceThreadReply(t, "unconfirmed-start", cwd, true, []string{root})
	unconfirmedFork := workspaceThreadReply(t, "unconfirmed-fork", cwd, false, []string{base})
	startCalls := 0
	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			startCalls++
			switch startCalls {
			case 1:
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent"}}`)}
			case 2:
				return jsonrpc.Response{ID: req.ID, Result: unconfirmedStart}
			default:
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "unsupported web search mode"}}
			}
		case "thread/fork":
			return jsonrpc.Response{ID: req.ID, Result: unconfirmedFork}
		default:
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	parent, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	invalid, err := c.StartThread(ctx, &types.ThreadOptions{NetworkAccessEnabled: &disabled})
	if invalid != nil || err == nil {
		t.Fatalf("unsupported sandbox request accepted: thread=%v error=%v", invalid, err)
	}
	opts := &types.ThreadOptions{
		Cwd: cwd, Sandbox: types.SandboxWorkspaceWrite,
		NetworkAccessEnabled: &disabled, AdditionalDirectories: []string{root},
	}
	started, err := c.StartThread(ctx, opts)
	if started != nil {
		t.Fatal("unconfirmed started thread was returned")
	}
	assertThreadPolicyMismatch(t, err, "unconfirmed-start", "sandbox.networkAccess")
	forked, identities, err := c.ForkThread(ctx, parent.ID(), opts)
	if forked != nil || identities != nil {
		t.Fatal("unconfirmed fork was returned")
	}
	assertThreadPolicyMismatch(t, err, "unconfirmed-fork", "sandbox.writableRoots")
	if got := c.SessionID(); got != parent.ID() {
		t.Fatalf("unconfirmed thread replaced the registered parent: %q", got)
	}
	_, err = c.StartThread(ctx, &types.ThreadOptions{WebSearchMode: types.WebSearchIndexed, ThreadSource: "custom"})
	var rpcError *types.RPCError
	if !errors.As(err, &rpcError) || rpcError.Code != -32602 || rpcError.Message != "unsupported web search mode" {
		t.Fatalf("provider rejection changed: %v", err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "thread/start", "thread/start", "thread/fork", "thread/start")
}

func TestClient_DirectReasoningEffortDoesNotLeak(t *testing.T) {
	t.Parallel()

	turnIDs := []string{"with-effort", "omit-effort"}
	turnCalls := 0
	c, server := setupMockClient(t, types.NewCodexOptions().WithReasoningEffort("medium"), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"reasoning"}}`)}
		case "turn/start":
			if turnCalls < len(turnIDs) {
				id := turnIDs[turnCalls]
				turnCalls++
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"` + id + `"}}`)}
			}
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "effort unsupported by model"}}
		default:
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	low := "low"
	mode := &types.CollaborationMode{
		Mode:     types.CollaborationModePlan,
		Settings: types.CollaborationModeSettings{Model: "selected-model", ReasoningEffort: &low},
	}
	runOpts := []*types.RunOptions{{ReasoningEffort: "high", CollaborationMode: mode}, nil}
	for i, opts := range runOpts {
		stream, runErr := thread.RunStreamed(ctx, "prompt", opts)
		if runErr != nil {
			t.Fatal(runErr)
		}
		server.push(notify("turn/completed", map[string]any{
			"threadId": "reasoning", "turn": map[string]any{"id": turnIDs[i], "status": "completed", "items": []any{}},
		}))
		var completed bool
		for event := range stream {
			if terminal, ok := event.(*types.TurnCompleted); ok {
				completed = terminal.TurnID == turnIDs[i]
			}
		}
		if !completed {
			t.Fatalf("turn %q did not complete", turnIDs[i])
		}
	}
	_, err = thread.RunStreamed(ctx, "prompt", &types.RunOptions{ReasoningEffort: "unknown-effort"})
	var rpcError *types.RPCError
	if !errors.As(err, &rpcError) || rpcError.Code != -32602 {
		t.Fatalf("model rejection changed: %v", err)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "thread/start", "turn/start", "turn/start", "turn/start")
	assertThreadConfigJSON(t, wire[1].Params, `{
		"threadId":"reasoning","input":[{"type":"text","text":"prompt"}],"effort":"high",
		"collaborationMode":{"mode":"plan","settings":{"model":"selected-model","reasoning_effort":"low"}}
	}`)
	assertThreadConfigJSON(t, wire[2].Params, `{"threadId":"reasoning","input":[{"type":"text","text":"prompt"}]}`)
	assertThreadConfigJSON(t, wire[3].Params, `{"threadId":"reasoning","input":[{"type":"text","text":"prompt"}],"effort":"unknown-effort"}`)
}

func workspaceThreadReply(t *testing.T, id, cwd string, network bool, roots []string) json.RawMessage {
	t.Helper()
	return threadConfigFixture(t, map[string]any{
		"thread": map[string]string{"id": id}, "cwd": cwd,
		"sandbox": map[string]any{"type": "workspaceWrite", "networkAccess": network, "writableRoots": roots},
	})
}

func threadConfigFixture(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func assertThreadPolicyMismatch(t *testing.T, err error, id, field string) {
	t.Helper()
	var mismatch *types.ThreadPolicyMismatchError
	if !errors.As(err, &mismatch) || mismatch.ThreadID != id || mismatch.Field != field {
		t.Fatalf("error=%v, want policy mismatch on %q for thread %q", err, field, id)
	}
}

func observedThreadRequests(t *testing.T, server *mockCodexServer) []jsonrpc.Request {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	requests := make([]jsonrpc.Request, 0, len(server.received))
	for _, frame := range server.received {
		var req jsonrpc.Request
		if err := json.Unmarshal(frame, &req); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, req)
	}
	return requests
}

func assertThreadRequestMethods(t *testing.T, wire []jsonrpc.Request, methods ...string) {
	t.Helper()
	if len(wire) != len(methods) {
		t.Fatalf("got %d requests, want %d (%v)", len(wire), len(methods), methods)
	}
	for i, method := range methods {
		if wire[i].Method != method {
			t.Fatalf("request %d = %q, want %q", i, wire[i].Method, method)
		}
	}
}
