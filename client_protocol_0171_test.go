package codex

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestSubscribeThreadEvents_Protocol0171RawAndGlobalNotifications(t *testing.T) {
	t.Parallel()

	obs := &clientRecordingObserver{}
	client, server := setupMockClient(t, types.NewCodexOptions().WithObserver(obs), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	global := []struct {
		method string
		params string
	}{
		{"project/changed", `{"changeType":"updated","projectId":"project"}`},
		{"thread/search/partial", `{"data":[],"requestId":5}`},
		{"mcpServer/event/stream/notification", `{"notification":{"method":"notifications/tools/list_changed","params":{}},"subscriptionId":"subscription"}`},
		{"account/gatewayOAuth/changed", `{"providerId":"provider","status":"succeeded"}`},
	}
	wantDiagnostics := make([]string, 0, len(global)+1)
	for _, n := range global {
		server.push(notify(n.method, json.RawMessage(n.params)))
		wantDiagnostics = append(wantDiagnostics, n.method)
	}

	const rawMethod = "thread/realtime/item/transcript/delta"
	const rawParams = `{"delta":"hello","itemId":"item","threadId":"child"}`
	server.push(notify(rawMethod, json.RawMessage(rawParams)))
	wantDiagnostics = append(wantDiagnostics, rawMethod)

	select {
	case envelope, ok := <-stream:
		if !ok || envelope.Err != nil || envelope.ThreadID != "child" {
			t.Fatalf("subscription event = %+v, open = %t", envelope, ok)
		}
		raw, ok := envelope.Event.(*types.UnknownEvent)
		if !ok || raw.Method != rawMethod || raw.ItemID != "item" || string(raw.Params) != rawParams {
			t.Fatalf("event = %#v, want raw %q with item ID and original params", envelope.Event, rawMethod)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for raw thread event")
	}

	// The thread event follows the four global frames through the same
	// dispatcher and serves as the delivery barrier for diagnostics.
	if got := obs.unknownSnapshot(); !slices.Equal(got, wantDiagnostics) {
		t.Fatalf("unknown diagnostics = %v, want %v", got, wantDiagnostics)
	}
}

func TestSubscribeThreadEvents_Protocol0171Metadata(t *testing.T) {
	t.Parallel()

	client, server := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.SubscribeThreadEvents(ctx, 4)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	for _, environmentID := range []string{"env-one", "env-two"} {
		server.push(notify("mcpServer/startupStatus/updated", map[string]string{
			"name": "shared", "status": "ready", "threadId": "child", "environmentId": environmentID,
		}))
	}
	server.push(notify("item/started", json.RawMessage(`{"threadId":"child","turnId":"turn","item":{"type":"subAgentActivity","id":"spawn","agentPath":"agents/reviewer","agentThreadId":"agent","kind":"started","model":"child-model","reasoningEffort":"high"}}`)))
	server.push(notify("item/completed", json.RawMessage(`{"threadId":"child","turnId":"turn","item":{"type":"functionCallOutput","id":"output","name":"inspect","namespace":"tools","output":[{"type":"input_text","text":"done"}]}}`)))

	nextEvent := func() types.ThreadEvent {
		t.Helper()
		select {
		case envelope, ok := <-stream:
			if !ok || envelope.Err != nil || envelope.ThreadID != "child" {
				t.Fatalf("subscription event = %+v, open = %t", envelope, ok)
			}
			return envelope.Event
		case <-ctx.Done():
			t.Fatal("timed out waiting for metadata event")
			return nil
		}
	}
	for _, environmentID := range []string{"env-one", "env-two"} {
		event := nextEvent()
		status, ok := event.(*types.MCPServerStartupStatusUpdated)
		if !ok || status.Name != "shared" || status.EnvironmentID == nil || *status.EnvironmentID != environmentID {
			t.Fatalf("event = %#v, want shared server in %q", event, environmentID)
		}
	}

	event := nextEvent()
	started, ok := event.(*types.ItemStarted)
	if !ok || started.ItemID != "spawn" {
		t.Fatalf("event = %#v, want sub-agent item started", event)
	}
	activity, ok := started.Item.(*types.SubAgentActivity)
	if !ok || activity.AgentThreadID != "agent" || activity.Model == nil || *activity.Model != "child-model" ||
		activity.ReasoningEffort == nil || *activity.ReasoningEffort != "high" {
		t.Fatalf("item = %#v, want child agent with model and effort", started.Item)
	}

	event = nextEvent()
	completed, ok := event.(*types.ItemCompleted)
	if !ok || completed.ItemID != "output" {
		t.Fatalf("event = %#v, want function output item completed", event)
	}
	output, ok := completed.Item.(*types.FunctionCallOutput)
	if !ok || output.Namespace == nil || *output.Namespace != "tools" ||
		string(output.Output) != `[{"type":"input_text","text":"done"}]` {
		t.Fatalf("item = %#v, want function output with raw content", completed.Item)
	}
}
