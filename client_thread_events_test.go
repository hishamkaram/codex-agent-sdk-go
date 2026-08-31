package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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

	client := &Client{
		threads:                make(map[string]*Thread),
		threadEventSubscribers: make(map[uint64]*threadEventSubscriber),
	}
	dispatcherCtx, dispatcherCancel := context.WithCancel(context.Background())
	defer dispatcherCancel()
	if err := client.startDispatcher(dispatcherCtx, dispatcherCancel, demux); err != nil {
		t.Fatalf("startDispatcher: %v", err)
	}
	select {
	case <-client.dispatcherDone:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not observe the closed event source")
	}
	if client.connected.Load() {
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
		c.handleNotification(jsonrpc.Notification{Method: "item/started", Params: params})
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
	c.handleNotification(jsonrpc.Notification{
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
	thread.eventMu.Lock()
	locked := true
	defer func() {
		if locked {
			thread.eventMu.Unlock()
		}
	}()

	notificationDone := make(chan struct{})
	go func() {
		defer close(notificationDone)
		c.handleNotification(jsonrpc.Notification{
			Method: "turn/started",
			Params: json.RawMessage(`{"threadId":"registered-thread","turn":{"id":"turn-1"}}`),
		})
	}()

	select {
	case event := <-events:
		thread.eventMu.Unlock()
		locked = false
		<-notificationDone
		t.Fatalf("subscriber observed event before registered thread state: %+v", event)
	case <-time.After(250 * time.Millisecond):
	}

	thread.eventMu.Unlock()
	locked = false
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
		c.handleNotification(jsonrpc.Notification{Method: "item/started", Params: params})
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

	c.handleNotification(jsonrpc.Notification{
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

	c.handleNotification(jsonrpc.Notification{
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

	c.handleNotification(jsonrpc.Notification{
		Method: "error",
		Params: json.RawMessage(`{"code":"legacy","message":"global warning"}`),
	})
	c.handleNotification(jsonrpc.Notification{
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
	if !c.connected.Load() {
		t.Fatal("malformed frame disconnected the parent client")
	}
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("parent RPC after malformed frame: %v", err)
	}
	if !c.connected.Load() {
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
	if !c.connected.Load() {
		t.Fatal("unclassifiable frame disconnected the parent client")
	}
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("parent RPC after unclassifiable frame: %v", err)
	}
	if !c.connected.Load() {
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
