package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const canonicalTestThreadID = "67e55044-10b1-426f-9247-bb680e5fe0c8"

func TestThreadExecutionID(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		canonicalTestThreadID,
		"67E5504410B1426F9247BB680E5FE0C8",
		"67E55044-10B1-426F-9247-BB680E5FE0C8",
		"{67E55044-10B1-426F-9247-BB680E5FE0C8}",
		"urn:uuid:67E55044-10B1-426F-9247-BB680E5FE0C8",
	} {
		if got := threadexec.CanonicalID(input); got != canonicalTestThreadID {
			t.Errorf("normalize %q = %q", input, got)
		}
	}
	for _, input := range []string{
		"opaque-provider-id",
		"67E55044-10B1-426F-9247-BB680E5FE0CG",
		"67E55044:10B1-426F-9247-BB680E5FE0C8",
		"URN:UUID:67E55044-10B1-426F-9247-BB680E5FE0C8",
	} {
		if got := threadexec.CanonicalID(input); got != input {
			t.Errorf("changed opaque ID %q to %q", input, got)
		}
	}
}

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
