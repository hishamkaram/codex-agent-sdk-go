package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// newThread constructs a Thread owned by c. Register before exposing it.
func newThread(c *Client, id string) *Thread {
	return &Thread{client: c, id: id, record: threadexec.NewRecord(false)}
}

func (c *Client) registerThread(t *Thread) {
	t.record = c.session.Threads().Register(t.id, t.record, c.session.Connected)
}

func TestClient_ForkThread_ProviderContract(t *testing.T) {
	t.Parallel()

	c, srv := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent"}}`)}
		case "thread/fork":
			// Independent v2 contract. A state-creating call using the old
			// spelling must fail rather than being accepted by the fake.
			var params struct {
				ThreadID       string          `json:"threadId"`
				SourceThreadID json.RawMessage `json:"sourceThreadId"`
				Model          string          `json:"model"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.ThreadID == "" || params.SourceThreadID != nil {
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "threadId is required"}}
			}
			if params.ThreadID == "parent" && params.Model == "invalid-child" {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent"}}`)}
			}
			if params.ThreadID != "parent" || params.Model != "gpt-5.4" {
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32000, Message: "unsupported fork options"}}
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"child"},"model":"gpt-5.4"}`)}
		case "turn/start":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			if json.Unmarshal(req.Params, &params) == nil && params.ThreadID == "parent" {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"parent-turn"}}`)}
			}
		}
		return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected request"}}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	parent, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	child, fork, err := c.ForkThread(ctx, parent.ID(), &types.ThreadOptions{Model: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	if child.ID() != "child" || parent.ID() != "parent" || fork.SourceThreadID != parent.ID() || fork.NewThreadID != child.ID() {
		t.Fatalf("parent = %q, child = %q, fork = %+v", parent.ID(), child.ID(), fork)
	}
	if _, _, err = c.ForkThread(ctx, "", nil); err == nil {
		t.Fatal("fork without a source was accepted")
	}
	_, _, err = c.ForkThread(ctx, parent.ID(), &types.ThreadOptions{Model: "unsupported"})
	var rpcErr *types.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32000 || rpcErr.Message != "unsupported fork options" {
		t.Fatalf("provider rejection = %v", err)
	}
	if _, _, err = c.ForkThread(ctx, parent.ID(), &types.ThreadOptions{Model: "invalid-child"}); err == nil {
		t.Fatal("fork response with the parent ID was accepted as a child")
	}

	stream, err := parent.RunStreamed(ctx, "continue original", nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.push(json.RawMessage(`{"method":"turn/completed","params":{"threadId":"parent","turn":{"id":"parent-turn","status":"completed","items":[]}}}`))
	select {
	case event := <-stream:
		completed, ok := event.(*types.TurnCompleted)
		if !ok || completed.TurnID != "parent-turn" {
			t.Fatalf("parent completion = %#v", event)
		}
	case <-ctx.Done():
		t.Fatal("parent did not receive its completion")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	var forks int
	for _, frame := range srv.received {
		var req jsonrpc.Request
		if json.Unmarshal(frame, &req) == nil && req.Method == "thread/fork" {
			forks++
		}
	}
	if forks != 3 {
		t.Fatalf("got %d fork requests, want only the three attempts with a source", forks)
	}
}

const canonicalTestThreadID = "67e55044-10b1-426f-9247-bb680e5fe0c8"

func TestClient_AliasResumeFencesCanonicalHandle(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	const alias = "67E5504410B1426F9247BB680E5FE0C8"
	resumeRequest := make(chan jsonrpc.Request, 1)
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"` + canonicalTestThreadID + `"}}`)}
		case "thread/resume":
			resumeRequest <- req
			return jsonrpc.Response{}
		default:
			t.Errorf("in-flight alias resume allowed %s", req.Method)
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"would-steer"}}`)}
		}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumed := make(chan error, 1)
	go func() {
		_, resumeErr := c.ResumeThread(ctx, alias, nil)
		resumed <- resumeErr
	}()
	req := awaitExecutionTest(t, ctx, resumeRequest)
	var params map[string]string
	if decodeErr := json.Unmarshal(req.Params, &params); decodeErr != nil || params["threadId"] != alias {
		t.Fatalf("resume must retain original wire ID: %s, %v", req.Params, decodeErr)
	}
	attempt, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, runErr := thread.RunStreamed(attempt, "would race aliased resume", nil); !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("aliased resume did not reserve the canonical handle: %v", runErr)
	}
	peer.push(jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"` + canonicalTestThreadID + `","status":{"type":"idle"}}}`)})
	if resumeErr := awaitExecutionTest(t, ctx, resumed); resumeErr != nil {
		t.Fatal(resumeErr)
	}
}

func TestClient_ColdAliasResumeRetainsCanonicalTerminal(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	resumeRequest := make(chan jsonrpc.Request, 1)
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/resume":
			resumeRequest <- req
			return jsonrpc.Response{}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"new"}}`)}
		default:
			t.Errorf("unexpected provider request %s", req.Method)
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	type resumeResult struct {
		thread *Thread
		err    error
	}
	resumed := make(chan resumeResult, 1)
	go func() {
		thread, resumeErr := c.ResumeThread(ctx, "{67E55044-10B1-426F-9247-BB680E5FE0C8}", nil)
		resumed <- resumeResult{thread: thread, err: resumeErr}
	}()
	req := awaitExecutionTest(t, ctx, resumeRequest)
	peer.push(notify("thread/status/changed", map[string]any{"threadId": canonicalTestThreadID, "status": "active"}))
	peer.push(notify("turn/completed", map[string]string{"threadId": canonicalTestThreadID, "turnId": "old", "status": "completed"}))
	awaitExecutionTest(t, ctx, observed)
	awaitExecutionTest(t, ctx, observed)
	peer.push(jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"` + canonicalTestThreadID + `","status":{"type":"active"},"turns":[{"id":"old","status":"inProgress"}]}}`)})
	loaded := awaitExecutionTest(t, ctx, resumed)
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}
	out, err := loaded.thread.RunStreamed(ctx, "after the same thread's terminal", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": canonicalTestThreadID, "turnId": "new", "status": "completed"}))
	if terminal, ok := awaitExecutionTest(t, ctx, out).(*types.TurnCompleted); !ok || terminal.TurnID != "new" {
		t.Fatalf("new operation terminal = %#v", terminal)
	}
}

func TestClient_ThreadConfigurationWireScope(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	cwd := filepath.Join(base, "work")
	root := filepath.Join(base, "sibling") + string(filepath.Separator)
	sourceReply := workspaceThreadReply(t, "source", cwd, false, []string{root})
	forkReply := workspaceThreadReply(t, "fork", cwd, true, []string{})

	defaults := types.NewCodexOptions().WithSandbox(types.SandboxWorkspaceWrite).WithModel("startup-model").
		WithWebSearchMode(types.WebSearchCached).WithReasoningEffort("medium").
		WithMCPServers(map[string]types.McpServerConfig{
			"default": types.McpStdioConfig{Command: "default-mcp"},
		})
	startCalls := 0
	c, server := setupMockClient(t, defaults, func(req jsonrpc.Request) jsonrpc.Response {
		var result json.RawMessage
		switch req.Method {
		case "configRequirements/read":
			result = json.RawMessage(`{"requirements":{"allowedWebSearchModes":["disabled","cached","indexed","live"]}}`)
		case "thread/start":
			startCalls++
			if startCalls == 1 {
				result = sourceReply
			} else {
				result = json.RawMessage(`{"thread":{"id":"second"}}`)
			}
		case "thread/fork":
			result = forkReply
		case "thread/read":
			result = json.RawMessage(`{"thread":{"id":"source","model":"configured-model","createdAt":1234}}`)
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
	assertThreadRequestMethods(t, wire,
		"configRequirements/read", "thread/start",
		"configRequirements/read", "thread/start",
		"configRequirements/read", "thread/fork",
		"configRequirements/read", "thread/read", "thread/resume",
	)
	assertThreadConfigJSON(t, wire[1].Params, string(threadConfigFixture(t, map[string]any{
		"cwd": cwd, "model": "configured-model", "sandbox": "workspace-write", "approvalPolicy": "never", "threadSource": "interactive",
		"config": map[string]any{
			"web_search": "disabled", "model_reasoning_effort": "high",
			"mcp_servers":             map[string]any{"local": map[string]string{"command": "local-mcp"}},
			"sandbox_workspace_write": map[string]any{"network_access": false, "writable_roots": []string{root}},
		},
	})))
	assertThreadConfigJSON(t, wire[3].Params, `{
		"sandbox":"workspace-write","approvalPolicy":"on-request","model":"startup-model",
		"config":{"web_search":"cached","model_reasoning_effort":"medium","mcp_servers":{"default":{"command":"default-mcp"}}}
	}`)
	assertThreadConfigJSON(t, wire[5].Params, string(threadConfigFixture(t, map[string]any{
		"threadId": "source", "cwd": cwd, "sandbox": "workspace-write", "approvalPolicy": "on-request", "model": "startup-model", "threadSource": "scheduled:daily",
		"config": map[string]any{
			"web_search": "indexed", "model_reasoning_effort": "medium",
			"mcp_servers":             map[string]any{"default": map[string]string{"command": "default-mcp"}},
			"sandbox_workspace_write": map[string]any{"network_access": true, "writable_roots": []string{}},
		},
	})))
	assertThreadConfigJSON(t, wire[7].Params, `{"threadId":"source","includeTurns":false}`)
	assertThreadConfigJSON(t, wire[8].Params, `{"threadId":"source","model":"configured-model","config":{"web_search":"live","model_reasoning_effort":"low"}}`)
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
		case "configRequirements/read":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"requirements":null}`)}
		case "thread/start":
			startCalls++
			switch startCalls {
			case 1:
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent"}}`)}
			case 2:
				return jsonrpc.Response{ID: req.ID, Result: unconfirmedStart}
			case 3:
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "unsupported web search mode"}}
			default:
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "unsupported thread source"}}
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
	_, err = c.StartThread(ctx, &types.ThreadOptions{WebSearchMode: types.WebSearchIndexed})
	var rpcError *types.RPCError
	if !errors.As(err, &rpcError) || rpcError.Code != -32602 || rpcError.Message != "unsupported web search mode" {
		t.Fatalf("provider rejection changed: %v", err)
	}
	_, err = c.StartThread(ctx, &types.ThreadOptions{ThreadSource: "custom"})
	if !errors.As(err, &rpcError) || rpcError.Code != -32602 || rpcError.Message != "unsupported thread source" {
		t.Fatalf("provider source rejection changed: %v", err)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "thread/start", "thread/start", "thread/fork", "configRequirements/read", "thread/start", "thread/start")
	assertThreadConfigJSON(t, wire[5].Params, `{"sandbox":"read-only","approvalPolicy":"on-request","threadSource":"custom"}`)
}

func TestClient_ResumeLoadedThreadReportsUnappliedEffort(t *testing.T) {
	t.Parallel()

	const canonical = "01234567-89ab-4cde-8abc-0123456789ab"
	c, server := setupMockClient(t, types.NewCodexOptions().WithReasoningEffort("medium"), func(req jsonrpc.Request) jsonrpc.Response {
		var result json.RawMessage
		switch req.Method {
		case "configRequirements/read":
			result = json.RawMessage(`{"requirements":{"allowedWebSearchModes":null}}`)
		case "thread/read":
			result = json.RawMessage(`{"thread":{"id":"` + canonical + `","model":"persisted-model"}}`)
		case "thread/resume":
			result = json.RawMessage(`{"thread":{"id":"` + canonical + `","status":{"type":"active"}},"reasoningEffort":"medium"}`)
		default:
			result = json.RawMessage(`{"thread":{"id":"` + canonical + `"},"reasoningEffort":"medium"}`)
		}
		return jsonrpc.Response{ID: req.ID, Result: result}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	alias := strings.ToUpper(canonical)
	resumed, err := c.ResumeThread(ctx, alias, &types.ResumeOptions{
		ReasoningEffort: "high", WebSearchMode: types.WebSearchDisabled,
	})
	var mismatch *types.ThreadResumeSettingsMismatchError
	if resumed != nil || !errors.As(err, &mismatch) || mismatch.ThreadID != thread.ID() {
		t.Fatalf("loaded resume: thread=%v error=%v", resumed, err)
	}
	if got := c.SessionID(); got != thread.ID() {
		t.Fatalf("resume mismatch changed the current thread: %q", got)
	}
	if _, runErr := thread.RunStreamed(ctx, "must not steer provider work", nil); !errors.Is(runErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("resume mismatch discarded active state: %v", runErr)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "thread/start", "configRequirements/read", "thread/read", "thread/resume")
	assertThreadConfigJSON(t, wire[2].Params, `{"threadId":"`+alias+`","includeTurns":false}`)
	assertThreadConfigJSON(t, wire[3].Params, `{"threadId":"`+alias+`","model":"persisted-model","config":{"model_reasoning_effort":"high","web_search":"disabled"}}`)
}

func TestClient_ResumeReasoningRequiresReadableModel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		reply jsonrpc.Response
	}{
		{"legacy model omitted", jsonrpc.Response{Result: json.RawMessage(`{"thread":{"id":"saved","createdAt":1234}}`)}},
		{"model null", jsonrpc.Response{Result: json.RawMessage(`{"thread":{"id":"saved","model":null}}`)}},
		{"model malformed", jsonrpc.Response{Result: json.RawMessage(`{"thread":{"id":"saved","model":123}}`)}},
		{"provider error", jsonrpc.Response{Error: &jsonrpc.RPCError{Code: -32601, Message: "method not found"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, server := setupMockClient(t, types.NewCodexOptions().WithReasoningEffort("high"), func(req jsonrpc.Request) jsonrpc.Response {
				reply := tc.reply
				reply.ID = req.ID
				return reply
			})
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			thread, err := c.ResumeThread(ctx, "saved", nil)
			if thread != nil {
				t.Fatal("resume proceeded without a readable model")
			}
			if tc.reply.Error != nil {
				var rpcErr *types.RPCError
				if !errors.As(err, &rpcErr) || rpcErr.Code != tc.reply.Error.Code || rpcErr.Message != tc.reply.Error.Message {
					t.Fatalf("provider error changed: %v", err)
				}
			} else {
				var typedErr *types.ThreadResumeModelUnavailableError
				if !errors.As(err, &typedErr) || typedErr.ThreadID != "saved" {
					t.Fatalf("error=%v, want model-unavailable error for saved thread", err)
				}
			}
			assertThreadRequestMethods(t, observedThreadRequests(t, server), "thread/read")
		})
	}
}

func TestClient_ResumeWithoutReasoningSkipsModelRead(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"saved"}}`)}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	if _, err := c.ResumeThread(ctx, "saved", nil); err != nil {
		t.Fatal(err)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "thread/resume")
	assertThreadConfigJSON(t, wire[0].Params, `{"threadId":"saved"}`)
}

func TestClient_ManagedWebSearchPolicyRejectsBeforeDispatch(t *testing.T) {
	t.Parallel()

	defaults := types.NewCodexOptions().WithWebSearchMode(types.WebSearchDisabled)
	c, server := setupMockClient(t, defaults, func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method == "configRequirements/read" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"requirements":{"allowedWebSearchModes":["live"]}}`)}
		}
		// A managed peer could reply successfully while substituting live.
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"unexpected"}}`)}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	started, startErr := c.StartThread(ctx, nil)
	resumed, resumeErr := c.ResumeThread(ctx, "loaded", &types.ResumeOptions{WebSearchMode: types.WebSearchDisabled})
	forked, fork, forkErr := c.ForkThread(ctx, "loaded", &types.ThreadOptions{WebSearchMode: types.WebSearchCached})
	if started != nil || resumed != nil || forked != nil || fork != nil {
		t.Fatal("managed mode rejection exposed a thread handle")
	}
	for _, tc := range []struct {
		mode types.WebSearchMode
		err  error
	}{
		{types.WebSearchDisabled, startErr},
		{types.WebSearchDisabled, resumeErr},
		{types.WebSearchCached, forkErr},
	} {
		var rejected *types.WebSearchModeDisallowedError
		if !errors.As(tc.err, &rejected) || rejected.Mode != tc.mode {
			t.Errorf("error = %v, want managed policy rejection of %q", tc.err, tc.mode)
		}
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server),
		"configRequirements/read", "configRequirements/read", "configRequirements/read")
}

func TestClient_WebSearchRequirementErrorsReachCaller(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "method not found"}}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	started, err := c.StartThread(ctx, &types.ThreadOptions{WebSearchMode: types.WebSearchLive})
	if started != nil || !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("got thread=%v error=%v, want unsupported requirements error", started, err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "configRequirements/read")
}

func TestClient_PolicyBearingCwdRejectsAmbiguousParentTraversal(t *testing.T) {
	t.Parallel()

	sep := string(filepath.Separator)
	root := filepath.VolumeName(t.TempDir()) + sep
	falseReadback := workspaceThreadReply(t, "wrong-directory", root, false, []string{})
	c, server := setupMockClient(t, types.NewCodexOptions().WithSandbox(types.SandboxWorkspaceWrite), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: falseReadback}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	// On Unix /bin can be a symlink to /usr/bin. Cleaning /bin/.. to /
	// obscures the disagreement with the directory used by native lookup.
	opts := &types.ThreadOptions{Cwd: root + "bin" + sep + "..", AdditionalDirectories: []string{}}
	started, startErr := c.StartThread(ctx, opts)
	forked, fork, forkErr := c.ForkThread(ctx, "parent", opts)
	if started != nil || forked != nil || fork != nil || startErr == nil || forkErr == nil {
		t.Fatalf("ambiguity accepted: start=%v fork=%v errors=(%v, %v)", started, forked, startErr, forkErr)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server))
}

func TestClient_RelativeWorkspaceCwdUsesAppServerLaunchDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	launchParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	launchCwd := filepath.Join(launchParent, "work")
	if mkdirErr := os.Mkdir(launchCwd, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	alias := filepath.Join(t.TempDir(), "launch-alias")
	if linkErr := os.Symlink(launchParent, alias); linkErr != nil {
		t.Fatal(linkErr)
	}
	t.Chdir(alias)
	clientCwd := t.TempDir()
	want := filepath.Join(launchCwd, "project")
	fixtureDir := t.TempDir()
	marker := filepath.Join(fixtureDir, "cwd")
	forkRequest := filepath.Join(fixtureDir, "fork-request")
	helper := filepath.Join(fixtureDir, "codex-helper")
	reply := threadConfigFixture(t, map[string]any{
		"id":     2,
		"result": workspaceThreadReply(t, "relative", want, false, []string{}),
	})
	forkReply := threadConfigFixture(t, map[string]any{
		"id":     3,
		"result": workspaceThreadReply(t, "inherited", launchCwd, false, []string{}),
	})
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex 0.171.0"; exit 0; fi
pwd -P > %q
IFS= read -r _initialize
printf '%%s
' '{"id":1,"result":{}}'
IFS= read -r _initialized
IFS= read -r _start
printf '%%s
' %q
IFS= read -r fork
printf '%%s
' "$fork" > %q
printf '%%s
' %q
while IFS= read -r _line; do :; done
`, marker, string(reply), forkRequest, string(forkReply))
	writeClientExecutable(t, helper, script)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	c, err := NewClient(ctx, types.NewCodexOptions().WithCLIPath(helper).WithSandbox(types.SandboxWorkspaceWrite).WithCwd("work"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = c.Close(closeCtx)
	})
	if connectErr := c.Connect(ctx); connectErr != nil {
		t.Fatal(connectErr)
	}
	t.Chdir(clientCwd)
	falseValue := false
	homeRelative := &types.ThreadOptions{Cwd: "~/project", NetworkAccessEnabled: &falseValue}
	if started, pathErr := c.StartThread(ctx, homeRelative); started != nil || pathErr == nil || !strings.Contains(pathErr.Error(), "home-relative") {
		t.Fatalf("home-relative start: thread=%v error=%v", started, pathErr)
	}
	if forked, fork, pathErr := c.ForkThread(ctx, "parent", homeRelative); forked != nil || fork != nil || pathErr == nil || !strings.Contains(pathErr.Error(), "home-relative") {
		t.Fatalf("home-relative fork: thread=%v fork=%v error=%v", forked, fork, pathErr)
	}
	got, err := c.StartThread(ctx, &types.ThreadOptions{Cwd: "project", NetworkAccessEnabled: &falseValue})
	if err != nil || got.Cwd() != want {
		t.Fatalf("StartThread got %v, %v, want cwd %q", got, err, want)
	}
	inherited, _, err := c.ForkThread(ctx, got.ID(), &types.ThreadOptions{NetworkAccessEnabled: &falseValue})
	if err != nil || inherited.Cwd() != launchCwd {
		t.Fatalf("ForkThread got %v, %v, want inherited cwd %q", inherited, err, launchCwd)
	}
	forkWire, err := os.ReadFile(forkRequest)
	if err != nil {
		t.Fatal(err)
	}
	var forkCall jsonrpc.Request
	if decodeErr := json.Unmarshal(forkWire, &forkCall); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	var forkParams struct {
		Cwd string `json:"cwd"`
	}
	if decodeErr := json.Unmarshal(forkCall.Params, &forkParams); decodeErr != nil || forkParams.Cwd != launchCwd {
		t.Fatalf("fork cwd = %q, decode error = %v, want %q", forkParams.Cwd, decodeErr, launchCwd)
	}
	actual, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(actual)) != launchCwd {
		t.Fatalf("subprocess launch directory marker = %q, %v, want %q", actual, err, launchCwd)
	}
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

func TestClient_MCPServerConfigWire(t *testing.T) {
	t.Parallel()

	defaults := map[string]types.McpServerConfig{
		"local": types.McpStdioConfig{
			Command:              "local-mcp",
			Args:                 []string{"--stdio"},
			EnvironmentVariables: map[string]string{"SDK_MCP_SENTINEL": "config-only"},
			StartupTimeoutMs:     275,
			ToolTimeoutMs:        1250,
			DefaultApprovalMode:  types.ApprovalPolicy("prompt"),
		},
	}
	overrides := map[string]types.McpServerConfig{
		"remote": &types.McpStreamableHTTPConfig{
			URL:                 "https://mcp.example.test/path",
			AuthType:            "oauth",
			OAuthCallbackURI:    "http://127.0.0.1:12345/callback",
			Headers:             map[string]string{"X-MCP-Sentinel": "config-only"},
			DefaultApprovalMode: types.ApprovalPolicy("writes"),
		},
	}
	c, server := setupMockClient(t, types.NewCodexOptions().WithMCPServers(defaults).WithReasoningEffort("medium"),
		func(req jsonrpc.Request) jsonrpc.Response {
			switch req.Method {
			case "thread/start":
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"source"}}`)}
			case "thread/fork":
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"fork"}}`)}
			default:
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
			}
		})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	source, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ForkThread(ctx, source.ID(), &types.ThreadOptions{MCPServers: overrides}); err != nil {
		t.Fatal(err)
	}

	requests := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, requests, "thread/start", "thread/fork")
	assertThreadConfigJSON(t, requests[0].Params, `{
		"sandbox":"read-only", "approvalPolicy":"on-request",
		"config":{
			"model_reasoning_effort":"medium",
			"mcp_servers":{"local":{
				"command":"local-mcp", "args":["--stdio"], "env":{"SDK_MCP_SENTINEL":"config-only"},
				"startup_timeout_ms":275, "tool_timeout_sec":1.25, "default_tools_approval_mode":"prompt"
			}}
		}
	}`)
	assertThreadConfigJSON(t, requests[1].Params, `{
		"threadId":"source", "sandbox":"read-only", "approvalPolicy":"on-request",
		"config":{
			"model_reasoning_effort":"medium",
			"mcp_servers":{"remote":{
				"url":"https://mcp.example.test/path", "auth":"oauth",
				"oauth":{"callback_url":"http://127.0.0.1:12345/callback"},
				"http_headers":{"X-MCP-Sentinel":"config-only"}, "default_tools_approval_mode":"writes"
			}}
		}
	}`)
}

func TestClient_MCPServerConfigRejectsBeforeDispatch(t *testing.T) {
	t.Parallel()

	type stdioWithHTTPSettings struct {
		types.McpStdioConfig
		Auth types.MCPHTTPAuth
	}

	const endpoint = "https://mcp.example.test"
	const secret = "synthetic-private-value"
	for _, tc := range []struct {
		name   string
		config types.McpServerConfig
		err    string
	}{
		{"typed nil", (*types.McpStdioConfig)(nil), "non-nil"},
		{"mixed transports", stdioWithHTTPSettings{McpStdioConfig: types.McpStdioConfig{Command: "local-mcp"}, Auth: types.MCPHTTPAuthOAuth}, "McpStdioConfig or McpStreamableHTTPConfig"},
		{"empty stdio command", types.McpStdioConfig{}, "Command"},
		{"negative timeout", types.McpStdioConfig{Command: "local-mcp", StartupTimeoutMs: -1}, "negative"},
		{"general Codex approval", types.McpStdioConfig{Command: "local-mcp", DefaultApprovalMode: types.ApprovalOnRequest}, "types.MCPToolApprovalPrompt"},
		{"invalid MCP approval", types.McpStdioConfig{Command: "local-mcp", ToolApprovalMode: types.MCPToolApprovalMode(secret)}, "approval mode"},
		{"conflicting approvals", types.McpStdioConfig{Command: "local-mcp", DefaultApprovalMode: types.ApprovalPolicy("auto"), ToolApprovalMode: types.MCPToolApprovalPrompt}, "conflicts"},
		{"raw token", types.McpStreamableHTTPConfig{URL: endpoint, BearerToken: secret}, "BearerTokenEnvVar"},
		{"malformed URL", types.McpStreamableHTTPConfig{URL: "https://mcp.example.test/" + secret + "%"}, "URL"},
		{"legacy bearer without env", types.McpStreamableHTTPConfig{URL: endpoint, AuthType: "bearer"}, "BearerTokenEnvVar"},
		{"legacy auth flow", types.McpStreamableHTTPConfig{URL: endpoint, AuthType: "chatgpt"}, "legacy AuthType"},
		{"canonical bearer auth", types.McpStreamableHTTPConfig{URL: endpoint, Auth: types.MCPHTTPAuth("bearer")}, "Auth"},
		{"ambiguous auth", types.McpStreamableHTTPConfig{URL: endpoint, AuthType: "oauth", Auth: types.MCPHTTPAuthOAuth}, "only one"},
		{"invalid env name", types.McpStreamableHTTPConfig{URL: endpoint, BearerTokenEnvVar: "TOKEN=" + secret}, "environment variable"},
		{"header and env", types.McpStreamableHTTPConfig{URL: endpoint, BearerTokenEnvVar: "SDK_TOKEN", Headers: map[string]string{"authorization": secret}}, "conflicting"},
		{"duplicate auth headers", types.McpStreamableHTTPConfig{URL: endpoint, Headers: map[string]string{"Authorization": secret, "authorization": secret}}, "conflicting"},
		{"EMA with alternate credentials", types.McpStreamableHTTPConfig{URL: endpoint, Auth: types.MCPHTTPAuthEMA, BearerTokenEnvVar: "SDK_TOKEN"}, "alternate credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := &types.ThreadOptions{MCPServers: map[string]types.McpServerConfig{"invalid": tc.config}}
			c, server := setupMockClient(t, types.NewCodexOptions(), nil)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			_, startErr := c.StartThread(ctx, opts)
			_, _, forkErr := c.ForkThread(ctx, "source", opts)
			for _, err := range []error{startErr, forkErr} {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatal("invalid config error exposed a secret")
				}
			}
			assertThreadRequestMethods(t, observedThreadRequests(t, server))
		})
	}
}

func assertThreadConfigJSON(t *testing.T, got any, want string) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", raw, want)
	}
}

func TestThreadCommands_InputValidation(t *testing.T) {
	t.Parallel()
	// Input validation precedes closed-handle rejection on these commands.
	tests := []struct {
		name    string
		call    func(*Thread) error
		wantErr string
	}{
		{
			"Rollback negative",
			func(th *Thread) error { return th.Rollback(context.Background(), -1) },
			"numTurns must be >= 1",
		},
		{
			"Rollback zero",
			func(th *Thread) error { return th.Rollback(context.Background(), 0) },
			"numTurns must be >= 1",
		},
		{
			"SetName empty",
			func(th *Thread) error { return th.SetName(context.Background(), "") },
			"name must not be empty",
		},
		{
			"Steer empty",
			func(th *Thread) error { return th.Steer(context.Background(), "") },
			"text must not be empty",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.call(&Thread{})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestThread_Steer_NoActiveTurn(t *testing.T) {
	t.Parallel()
	// Thread with empty activeTurnID and a non-empty input should
	// return "no active turn" — same precondition as Interrupt.
	th := newThread(nil, "test")
	err := th.Steer(context.Background(), "extend the plan")
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("err = %v, want ErrNoActiveTurn", err)
	}
}

func TestThread_SteerSendsExpectedTurnID(t *testing.T) {
	t.Parallel()

	requests := make(chan jsonrpc.Request, 1)
	client, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method == "turn/steer" {
			requests <- req
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
		return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
	})
	thread := newThread(client, "thread-1")
	thread.record.Observe(&types.TurnStarted{ThreadID: thread.ID(), TurnID: "turn-1"})

	if err := thread.Steer(context.Background(), "include tests"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	var params map[string]any
	if err := json.Unmarshal((<-requests).Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["expectedTurnId"] != "turn-1" {
		t.Fatalf("expectedTurnId = %#v, want turn-1", params["expectedTurnId"])
	}
	if _, legacy := params["turnId"]; legacy {
		t.Fatal("legacy turnId must not be sent")
	}
}

func TestThread_Interrupt_NoActiveTurnIsRecoverableSentinel(t *testing.T) {
	t.Parallel()

	th := newThread(nil, "test")
	err := th.Interrupt(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("err = %v, want errors.Is ErrNoActiveTurn", err)
	}
}

func TestThread_ActiveTurnIDClearsOnTerminalTurnEvent(t *testing.T) {
	t.Parallel()

	th := newThread(nil, "test")
	th.record.Observe(&types.TurnStarted{ThreadID: th.ID(), TurnID: "turn-1"})
	th.record.Observe(&types.TurnCompleted{ThreadID: "test", TurnID: "turn-1", Status: "success"})

	got := th.record.ActiveID()
	if got != "" {
		t.Fatalf("activeTurnID = %q, want cleared", got)
	}
}

func TestThread_ActiveTurnIDIgnoresOlderTerminalTurnEvent(t *testing.T) {
	t.Parallel()

	th := newThread(nil, "test")
	th.record.Observe(&types.TurnStarted{ThreadID: th.ID(), TurnID: "turn-2"})
	th.record.Observe(&types.TurnCompleted{ThreadID: "test", TurnID: "turn-1", Status: "success"})

	got := th.record.ActiveID()
	if got != "turn-2" {
		t.Fatalf("activeTurnID = %q, want turn-2", got)
	}
}

func TestThreadRejectsClosedAndUninitializedHandles(t *testing.T) {
	t.Parallel()

	client, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"retired"}}`)}
		default:
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	ctx := t.Context()
	retired, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	retired.record.Observe(&types.TurnStarted{ThreadID: retired.ID(), TurnID: "active"})
	// Close the record while leaving the wire available to catch any send.
	retired.record.Close()

	// Both initialized and rejected handles carry the same public error.
	program := types.CyberAccessProgramStandard
	tests := []struct {
		name string
		call func(*Thread) error
	}{
		{"Rollback", func(th *Thread) error { return th.Rollback(ctx, 1) }},
		{"SetName", func(th *Thread) error { return th.SetName(ctx, "new") }},
		{"Steer", func(th *Thread) error { return th.Steer(ctx, "continue") }},
		{"Interrupt", func(th *Thread) error { return th.Interrupt(ctx) }},
		{"CleanBackgroundTerminals", func(th *Thread) error { return th.CleanBackgroundTerminals(ctx) }},
		{"RunStreamedWithProgram", func(th *Thread) error {
			_, err := th.RunStreamed(ctx, "prompt", &types.RunOptions{CyberAccessProgram: &program})
			return err
		}},
		{"Compact", func(th *Thread) error {
			_, err := th.Compact(ctx, nil)
			return err
		}},
		{"Summarize", func(th *Thread) error {
			_, err := th.Summarize(ctx)
			return err
		}},
		{"StartReview", func(th *Thread) error {
			_, err := th.StartReview(ctx, reviewOptsForTest())
			return err
		}},
		{"StartDetachedReview", func(th *Thread) error {
			opts := reviewOptsForTest()
			opts.Delivery = types.ReviewDetached
			_, err := th.StartReview(ctx, opts)
			return err
		}},
		{"StartReviewStreamed", func(th *Thread) error {
			_, err := th.StartReviewStreamed(ctx, reviewOptsForTest())
			return err
		}},
		{"GitDiff", func(th *Thread) error {
			_, err := th.GitDiff(ctx, nil)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run("closed/"+tt.name, func(t *testing.T) {
			if err := tt.call(retired); !errors.Is(err, types.ErrThreadClosed) {
				t.Errorf("closed thread error = %v, want ErrThreadClosed", err)
			}
		})
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, peer), "thread/start")
	for _, tt := range tests {
		t.Run("zero/"+tt.name, func(t *testing.T) {
			var zero Thread
			if err := tt.call(&zero); !errors.Is(err, types.ErrThreadClosed) {
				t.Errorf("zero thread error = %v, want ErrThreadClosed", err)
			}
		})
	}
}

func TestThread_StartReview_TargetRequired(t *testing.T) {
	t.Parallel()
	// Target zero-value → reject before reaching transport.
	th := newThread(nil, "test")
	_, err := th.StartReview(context.Background(), types.ReviewOptions{})
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	if !strings.Contains(err.Error(), "opts.Target.Type is required") {
		t.Errorf("err = %q", err)
	}
}

func TestThread_StartReviewStreamed_TargetRequired(t *testing.T) {
	t.Parallel()
	th := newThread(nil, "test")
	_, err := th.StartReviewStreamed(context.Background(), types.ReviewOptions{})
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	if !strings.Contains(err.Error(), "opts.Target.Type is required") {
		t.Errorf("err = %q", err)
	}
}

func TestThread_StartReviewStreamed_RejectsDetachedDelivery(t *testing.T) {
	t.Parallel()
	th := newThread(nil, "test")
	_, err := th.StartReviewStreamed(context.Background(), types.ReviewOptions{
		Target:   types.ReviewTargetUncommittedChanges(),
		Delivery: types.ReviewDetached,
	})
	if err == nil {
		t.Fatal("expected error for detached delivery")
	}
	if !strings.Contains(err.Error(), "detached review delivery is not streamable") {
		t.Errorf("err = %q", err)
	}
}

func TestCompactResult_NilSafe(t *testing.T) {
	t.Parallel()
	var r *CompactResult
	_, err := r.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on nil CompactResult")
	}
}

// reviewOptsForTest builds a minimal-valid ReviewOptions for
// closed-thread tests (no live RPC, so Target contents don't matter
// beyond non-empty Type).
func reviewOptsForTest() types.ReviewOptions {
	return types.ReviewOptions{Target: types.ReviewTargetUncommittedChanges()}
}
