package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
