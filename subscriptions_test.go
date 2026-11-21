package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestSubscribeThreadEventsRacingSourceLossGetsGapOrIsRejected(t *testing.T) {
	t.Parallel()

	client, peer := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type enrollment struct {
		events <-chan ThreadEventEnvelope
		err    error
	}
	start := make(chan struct{})
	subscribeReady := make(chan struct{})
	closeReady := make(chan struct{})
	peerClosed := make(chan struct{})
	result := make(chan enrollment, 1)
	go func() {
		close(subscribeReady)
		select {
		case <-start:
			events, err := client.SubscribeThreadEvents(ctx, 1)
			result <- enrollment{events: events, err: err}
		case <-ctx.Done():
		}
	}()
	go func() {
		defer close(peerClosed)
		close(closeReady)
		select {
		case <-start:
			peer.close()
		case <-ctx.Done():
		}
	}()

	awaitExecutionTest(t, ctx, subscribeReady)
	awaitExecutionTest(t, ctx, closeReady)
	close(start)
	got := awaitExecutionTest(t, ctx, result)
	awaitExecutionTest(t, ctx, peerClosed)
	awaitExecutionTest(t, ctx, client.session.DispatcherDone())

	if errors.Is(got.err, types.ErrClientNotConnected) && got.events == nil {
		return
	}
	if got.err != nil || got.events == nil {
		t.Fatalf("enroll during source loss: events=%v err=%v", got.events, got.err)
	}
	var gap *ThreadEventSourceGapError
	select {
	case event, ok := <-got.events:
		if !ok || event.Event != nil || event.ThreadID != "" || !errors.As(event.Err, &gap) || !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("admitted subscription received %+v (open=%v), want a terminal source gap", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("admitted subscription did not receive a source gap")
	}
	select {
	case _, ok := <-got.events:
		if ok {
			t.Fatal("subscription delivered an event after the source gap")
		}
	case <-ctx.Done():
		t.Fatal("subscription did not close after the source gap")
	}
}

func TestSubscribeThreadEventsCanResubscribeAfterRecoverableGap(t *testing.T) {
	t.Parallel()

	client, peer := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := client.SubscribeThreadEvents(ctx, 1)
	if err != nil {
		t.Fatalf("subscribe before malformed frame: %v", err)
	}
	if _, writeErr := peer.serverOut.Write([]byte("not-json\n")); writeErr != nil {
		t.Fatalf("write malformed frame: %v", writeErr)
	}

	var gap *ThreadEventFrameGapError
	select {
	case event, ok := <-first:
		if !ok || !errors.As(event.Err, &gap) || gap.Cause == nil {
			t.Fatalf("first subscription received %+v (open=%v), want a frame gap with its cause", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("first subscription did not receive a frame gap")
	}
	select {
	case _, ok := <-first:
		if ok {
			t.Fatal("first subscription remained open after its gap")
		}
	case <-ctx.Done():
		t.Fatal("first subscription did not close after its gap")
	}

	second, err := client.SubscribeThreadEvents(ctx, 1)
	if err != nil {
		t.Fatalf("subscribe after recoverable gap: %v", err)
	}
	peer.push(notify("turn/started", map[string]any{
		"threadId": "resubscribed",
		"turn":     map[string]any{"id": "next-turn", "status": "inProgress", "items": []any{}},
	}))

	select {
	case event, ok := <-second:
		turn, isStart := event.Event.(*types.TurnStarted)
		if !ok || event.Err != nil || event.ThreadID != "resubscribed" || !isStart || turn.TurnID != "next-turn" {
			t.Fatalf("second subscription received %+v (open=%v), want the next turn start", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("second subscription did not receive the next turn start")
	}
}

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

func TestStartDispatcherCannotPublishConnectedAfterSourceExit(t *testing.T) {
	demux := jsonrpc.NewDemux(
		jsonrpc.NewLineReader(strings.NewReader("")),
		jsonrpc.NewLineWriter(io.Discard),
		nil,
	)
	demux.Run(context.Background())
	<-demux.LoopError()
	for range demux.Notifications() {
	}
	for range demux.ServerRequests() {
	}

	client, err := NewClient(t.Context(), types.NewCodexOptions())
	if err != nil {
		t.Fatal(err)
	}
	dispatcherCtx, dispatcherCancel := context.WithCancel(context.Background())
	defer dispatcherCancel()
	if err := client.session.StartDispatcher(dispatcherCtx, dispatcherCancel, demux, client.session.Dispatch); err != nil {
		t.Fatalf("startDispatcher: %v", err)
	}
	select {
	case <-client.session.DispatcherDone():
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not observe the closed event source")
	}
	if client.session.Connected() {
		dispatcherCancel()
		t.Fatal("exited dispatcher left client connected")
	}
}

func TestSubscribeThreadEventsDeliversUnknownThreadFIFO(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := c.SubscribeThreadEvents(ctx, 4)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	for _, text := range []string{"one", "two"} {
		params, marshalErr := json.Marshal(map[string]any{
			"threadId": "unregistered-child",
			"turnId":   "turn-child",
			"item": map[string]any{
				"type": "agentMessage",
				"id":   "message-" + text,
				"text": text,
			},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		c.session.HandleNotification(jsonrpc.Notification{Method: "item/started", Params: params})
	}

	for _, want := range []string{"one", "two"} {
		select {
		case got := <-events:
			if got.Err != nil || got.ThreadID != "unregistered-child" {
				t.Fatalf("event = %+v", got)
			}
			started, ok := got.Event.(*types.ItemStarted)
			if !ok {
				t.Fatalf("event type = %T", got.Event)
			}
			message, ok := started.Item.(*types.AgentMessage)
			if !ok || message.Text != want {
				t.Fatalf("item = %#v, want text %q", started.Item, want)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for thread event")
		}
	}
}

func TestSubscribeThreadEventsDeliversThreadScopedError(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}
	c.session.HandleNotification(jsonrpc.Notification{
		Method: "error",
		Params: json.RawMessage(`{"threadId":"child","turnId":"turn","willRetry":false,"error":{"message":"failed"}}`),
	})

	select {
	case envelope := <-events:
		if envelope.Err != nil || envelope.ThreadID != "child" {
			t.Fatalf("envelope = %+v", envelope)
		}
		event, ok := envelope.Event.(*types.ErrorEvent)
		if !ok || event.TurnID != "turn" || event.Message != "failed" {
			t.Fatalf("event = %#v", envelope.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for thread error")
	}
}

func TestSubscribeThreadEventsObserveRegisteredThreadStateBeforeEvent(t *testing.T) {
	interruptSeen := make(chan struct{}, 1)
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method == "turn/interrupt" {
			interruptSeen <- struct{}{}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	thread := newThread(c, "registered-thread")
	c.registerThread(thread)
	release := holdExecutionGuard(t, t.Context(), thread)

	notificationDone := make(chan struct{})
	go func() {
		defer close(notificationDone)
		c.session.HandleNotification(jsonrpc.Notification{
			Method: "turn/started",
			Params: json.RawMessage(`{"threadId":"registered-thread","turn":{"id":"turn-1"}}`),
		})
	}()

	select {
	case event := <-events:
		release()
		<-notificationDone
		t.Fatalf("subscriber observed event before registered thread state: %+v", event)
	case <-time.After(250 * time.Millisecond):
	}

	release()
	select {
	case event := <-events:
		if event.ThreadID != thread.ID() {
			t.Fatalf("thread ID = %q, want %q", event.ThreadID, thread.ID())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn/started event")
	}
	<-notificationDone

	if err := thread.Interrupt(context.Background()); err != nil {
		t.Fatalf("Interrupt after turn/started: %v", err)
	}
	select {
	case <-interruptSeen:
	case <-time.After(time.Second):
		t.Fatal("turn/interrupt was not sent")
	}
}

func TestSubscribeThreadEventsOverflowReportsGapAndCloses(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 2)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	params := json.RawMessage(`{"threadId":"child","turnId":"turn","item":{"type":"agentMessage","id":"message","text":"x"}}`)
	for range 3 {
		c.session.HandleNotification(jsonrpc.Notification{Method: "item/started", Params: params})
	}

	var got []ThreadEventEnvelope
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want two events plus gap: %+v", len(got), got)
	}
	if got[0].Err != nil || got[1].Err != nil || !errors.Is(got[2].Err, ErrThreadEventStreamGap) {
		t.Fatalf("events = %+v", got)
	}
}

func TestSubscribeThreadEventsParseFailureReportsGapAndCloses(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	c.session.HandleNotification(jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"child","turnId":"turn"}`),
	})

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("subscription closed without reporting a gap")
		}
		if event.ThreadID != "child" || !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("event = %+v", event)
		}
		var parseGap *ThreadEventParseGapError
		if !errors.As(event.Err, &parseGap) || parseGap.Method != "item/started" {
			t.Fatalf("error = %v", event.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for parse gap")
	}

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("subscription remained open after parse gap")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after parse gap")
	}
}

func TestSubscribeThreadEventsMissingRequiredThreadIDReportsGap(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	c.session.HandleNotification(jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"turnId":"turn","item":{"type":"agentMessage","id":"message","text":"output"}}`),
	})

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("subscription closed without reporting an identity gap")
		}
		if event.ThreadID != "" || !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("event = %+v", event)
		}
		var parseGap *ThreadEventParseGapError
		if !errors.As(event.Err, &parseGap) || parseGap.Method != "item/started" || parseGap.Cause == nil {
			t.Fatalf("error = %v, want item/started parse gap", event.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for identity gap")
	}

	if _, ok := <-events; ok {
		t.Fatal("subscription remained open after identity gap")
	}
}

func TestSubscribeThreadEventsIdentitylessLegacyGlobalEventDoesNotGap(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	c.session.HandleNotification(jsonrpc.Notification{
		Method: "error",
		Params: json.RawMessage(`{"code":"legacy","message":"global warning"}`),
	})
	c.session.HandleNotification(jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"child","turnId":"turn","item":{"type":"agentMessage","id":"message","text":"output"}}`),
	})

	select {
	case event, ok := <-events:
		if !ok || event.Err != nil || event.ThreadID != "child" {
			t.Fatalf("event = %+v, open = %v", event, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event after identity-less global event")
	}
}

func TestSubscribeThreadEventsMalformedFrameReportsGapAndParentContinues(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}
	if _, err := server.serverOut.Write([]byte("not-json\n")); err != nil {
		t.Fatalf("write malformed frame: %v", err)
	}

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("subscription closed without reporting a frame gap")
		}
		if event.ThreadID != "" || !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("event = %+v", event)
		}
		var frameGap *ThreadEventFrameGapError
		if !errors.As(event.Err, &frameGap) || frameGap.Cause == nil {
			t.Fatalf("error = %v, want ThreadEventFrameGapError", event.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame gap")
	}

	if _, ok := <-events; ok {
		t.Fatal("subscription remained open after frame gap")
	}
	if !c.session.Connected() {
		t.Fatal("malformed frame disconnected the parent client")
	}
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("parent RPC after malformed frame: %v", err)
	}
	if !c.session.Connected() {
		t.Fatal("parent client disconnected after follow-up RPC")
	}
}

func TestSubscribeThreadEventsUnclassifiableFrameReportsGapAndParentContinues(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}
	if _, err := server.serverOut.Write([]byte("{\"params\":{\"threadId\":\"child\"}}\n")); err != nil {
		t.Fatalf("write unclassifiable frame: %v", err)
	}

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("subscription closed without reporting a frame gap")
		}
		if event.ThreadID != "" || !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("event = %+v", event)
		}
		var frameGap *ThreadEventFrameGapError
		if !errors.As(event.Err, &frameGap) || frameGap.Cause == nil {
			t.Fatalf("error = %v, want ThreadEventFrameGapError", event.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame gap")
	}

	if _, ok := <-events; ok {
		t.Fatal("subscription remained open after frame gap")
	}
	if !c.session.Connected() {
		t.Fatal("unclassifiable frame disconnected the parent client")
	}
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("parent RPC after unclassifiable frame: %v", err)
	}
	if !c.session.Connected() {
		t.Fatal("parent client disconnected after follow-up RPC")
	}
}

func TestSubscribeThreadEventsSourceClosureReportsGapAndCloses(t *testing.T) {
	c, server := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	server.close()

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("subscription closed without reporting a source gap")
		}
		if !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("event = %+v, want stream gap", event)
		}
		var sourceGap *ThreadEventSourceGapError
		if !errors.As(event.Err, &sourceGap) {
			t.Fatalf("error = %v, want ThreadEventSourceGapError", event.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for source gap")
	}

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("subscription remained open after source gap")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after source gap")
	}

	if _, err := c.SubscribeThreadEvents(context.Background(), 1); !errors.Is(err, types.ErrClientNotConnected) {
		t.Fatalf("SubscribeThreadEvents after source closure error = %v, want ErrClientNotConnected", err)
	}
}

func TestSubscribeThreadEventsSourceClosureDrainsBufferedNotificationsBeforeGap(t *testing.T) {
	c, server := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 3)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	for _, text := range []string{"one", "two"} {
		server.push(notify("item/started", map[string]any{
			"threadId": "child",
			"turnId":   "turn",
			"item": map[string]any{
				"type": "agentMessage",
				"id":   "message-" + text,
				"text": text,
			},
		}))
	}
	server.close()

	var got []ThreadEventEnvelope
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for events != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			got = append(got, event)
		case <-timeout.C:
			t.Fatal("subscription did not close after source gap")
		}
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want two notifications plus gap: %+v", len(got), got)
	}
	for index, want := range []string{"one", "two"} {
		started, ok := got[index].Event.(*types.ItemStarted)
		if !ok {
			t.Fatalf("event %d type = %T", index, got[index].Event)
		}
		message, ok := started.Item.(*types.AgentMessage)
		if !ok || message.Text != want {
			t.Fatalf("event %d item = %#v, want text %q", index, started.Item, want)
		}
	}
	if !errors.Is(got[2].Err, ErrThreadEventStreamGap) {
		t.Fatalf("terminal event = %+v, want stream gap", got[2])
	}
}

func TestSubscribeThreadEventsClientCloseDoesNotReportGap(t *testing.T) {
	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	events, err := c.SubscribeThreadEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case event, ok := <-events:
		if ok {
			t.Fatalf("client shutdown emitted event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close with client")
	}
}

func TestSubscribeThreadEventsClosesOnContextCancel(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	events, err := c.SubscribeThreadEvents(ctx, 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}
	cancel()

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("subscription remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after cancellation")
	}
}
