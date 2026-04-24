package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestParseEvent_ThreadStarted_NestedThreadID(t *testing.T) {
	t.Parallel()
	// Spike transcripts show thread/started with nested thread.id.
	n := jsonrpc.Notification{
		Method: "thread/started",
		Params: json.RawMessage(`{"thread":{"id":"T-abc"}}`),
	}
	ev, err := ParseEvent(n)
	if err != nil {
		t.Fatal(err)
	}
	ts, ok := ev.(*types.ThreadStarted)
	if !ok {
		t.Fatalf("got %T", ev)
	}
	if ts.ThreadID != "T-abc" {
		t.Fatalf("ThreadID = %q", ts.ThreadID)
	}
	if ts.EventMethod() != "thread/started" {
		t.Fatalf("EventMethod = %q", ts.EventMethod())
	}
}

func TestParseEvent_ThreadStarted_FlatThreadID(t *testing.T) {
	t.Parallel()
	// Alternate shape: flat threadId field.
	n := jsonrpc.Notification{
		Method: "thread/started",
		Params: json.RawMessage(`{"threadId":"T-flat"}`),
	}
	ev, _ := ParseEvent(n)
	ts := ev.(*types.ThreadStarted)
	if ts.ThreadID != "T-flat" {
		t.Fatalf("ThreadID = %q", ts.ThreadID)
	}
}

func TestParseEvent_TurnStarted(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "turn/started",
		Params: json.RawMessage(`{"thread":{"id":"T1"},"turn":{"id":"U1"}}`),
	}
	ev, _ := ParseEvent(n)
	ts := ev.(*types.TurnStarted)
	if ts.ThreadID != "T1" || ts.TurnID != "U1" {
		t.Fatalf("%+v", ts)
	}
}

func TestParseEvent_TurnCompleted_NestedRealShape(t *testing.T) {
	t.Parallel()
	// Real wire shape captured from CLI 0.121.0: status is nested inside
	// turn.status, usage is NOT present on this event (usage flows via
	// thread/tokenUsage/updated).
	n := jsonrpc.Notification{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"T","turn":{"id":"U","status":"success",` +
			`"startedAt":1776371820,"completedAt":1776371822,"durationMs":2194}}`),
	}
	ev, _ := ParseEvent(n)
	tc := ev.(*types.TurnCompleted)
	if tc.ThreadID != "T" || tc.TurnID != "U" || tc.Status != "success" {
		t.Fatalf("%+v", tc)
	}
}

func TestParseEvent_TurnCompleted_FlatFallbackShape(t *testing.T) {
	t.Parallel()
	// Forward-compat: older/other CLI versions may use flat turnId +
	// flat status + flat usage.
	n := jsonrpc.Notification{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","status":"success",` +
			`"usage":{"inputTokens":100,"outputTokens":50,"cachedInputTokens":20}}`),
	}
	ev, _ := ParseEvent(n)
	tc := ev.(*types.TurnCompleted)
	if tc.Status != "success" || tc.Usage.InputTokens != 100 ||
		tc.Usage.OutputTokens != 50 || tc.Usage.CachedInputTokens != 20 {
		t.Fatalf("%+v", tc)
	}
}

func TestParseEvent_TurnFailed(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "turn/failed",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","code":"ERR_X","message":"boom"}`),
	}
	ev, _ := ParseEvent(n)
	tf := ev.(*types.TurnFailed)
	if tf.Code != "ERR_X" || tf.Message != "boom" {
		t.Fatalf("%+v", tf)
	}
}

func TestParseEvent_ItemStartedWithAgentMessage(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","itemId":"I-1",` +
			`"item":{"type":"agentMessage","text":"Hello"}}`),
	}
	ev, err := ParseEvent(n)
	if err != nil {
		t.Fatal(err)
	}
	is := ev.(*types.ItemStarted)
	if is.ItemID != "I-1" {
		t.Fatalf("ItemID = %q", is.ItemID)
	}
	msg := is.Item.(*types.AgentMessage)
	if msg.Text != "Hello" {
		t.Fatalf("text = %q", msg.Text)
	}
}

func TestParseEvent_ItemCompletedWithCommandExecution(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","itemId":"I-2",` +
			`"item":{"type":"commandExecution","command":"ls","exitCode":0,` +
			`"aggregatedOutput":"f\n","status":"success"}}`),
	}
	ev, err := ParseEvent(n)
	if err != nil {
		t.Fatal(err)
	}
	ic := ev.(*types.ItemCompleted)
	cmd := ic.Item.(*types.CommandExecution)
	if cmd.Command != "ls" || cmd.ExitCode == nil || *cmd.ExitCode != 0 {
		t.Fatalf("%+v", cmd)
	}
	if cmd.AggregatedOutput != "f\n" {
		t.Fatalf("output = %q", cmd.AggregatedOutput)
	}
}

func TestParseEvent_ItemUpdatedWithDelta(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "item/updated",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","itemId":"I-1",` +
			`"delta":{"type":"agent_message_delta","text_chunk":" wo"}}`),
	}
	ev, _ := ParseEvent(n)
	iu := ev.(*types.ItemUpdated)
	d := iu.Delta.(*types.AgentMessageDelta)
	if d.TextChunk != " wo" {
		t.Fatalf("TextChunk = %q", d.TextChunk)
	}
}

func TestParseEvent_TokenUsageUpdated_RealShape(t *testing.T) {
	t.Parallel()
	// Real wire shape: tokenUsage.total (and .last) carry the running
	// snapshots. The SDK surfaces .total.
	n := jsonrpc.Notification{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U",` +
			`"tokenUsage":{"total":{"totalTokens":12632,"inputTokens":12615,` +
			`"cachedInputTokens":4480,"outputTokens":17,"reasoningOutputTokens":10},` +
			`"last":{"totalTokens":120,"inputTokens":100,"outputTokens":20},` +
			`"modelContextWindow":258400}}`),
	}
	ev, _ := ParseEvent(n)
	tu := ev.(*types.TokenUsageUpdated)
	if tu.Usage.TotalTokens != 12632 || tu.Usage.InputTokens != 12615 ||
		tu.Usage.CachedInputTokens != 4480 || tu.Usage.OutputTokens != 17 ||
		tu.Usage.ReasoningOutputTokens != 10 {
		t.Fatalf("%+v", tu.Usage)
	}
	if tu.ModelContextWindow != 258400 {
		t.Fatalf("ModelContextWindow = %d, want 258400", tu.ModelContextWindow)
	}
}

func TestParseEvent_TokenUsageUpdated_FlatFallback(t *testing.T) {
	t.Parallel()
	// Forward-compat for flat usage shape.
	n := jsonrpc.Notification{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"threadId":"T","usage":{"inputTokens":5,"outputTokens":2}}`),
	}
	ev, _ := ParseEvent(n)
	tu := ev.(*types.TokenUsageUpdated)
	if tu.Usage.InputTokens != 5 || tu.Usage.OutputTokens != 2 {
		t.Fatalf("%+v", tu.Usage)
	}
	if tu.ModelContextWindow != 0 {
		t.Fatalf("ModelContextWindow = %d, want 0", tu.ModelContextWindow)
	}
}

func TestParseEvent_ContextCompacted(t *testing.T) {
	t.Parallel()
	// Real wire method: thread/compacted.
	n := jsonrpc.Notification{
		Method: "thread/compacted",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U"}`),
	}
	ev, _ := ParseEvent(n)
	cc := ev.(*types.ContextCompacted)
	if cc.ThreadID != "T" || cc.TurnID != "U" {
		t.Fatalf("%+v", cc)
	}
}

// TestParseEvent_ContextCompactedLegacy confirms the old v0.1.0
// "compaction_event" method still resolves to ContextCompacted.
func TestParseEvent_ContextCompactedLegacy(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "compaction_event",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U"}`),
	}
	ev, _ := ParseEvent(n)
	if _, ok := ev.(*types.ContextCompacted); !ok {
		t.Fatalf("got %T, want *types.ContextCompacted", ev)
	}
}

func TestParseEvent_ErrorEvent(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "error",
		Params: json.RawMessage(`{"code":"CTX_OVERFLOW","message":"too big","context":{"tokens":9000}}`),
	}
	ev, _ := ParseEvent(n)
	ee := ev.(*types.ErrorEvent)
	if ee.Code != "CTX_OVERFLOW" || ee.Message != "too big" || len(ee.Context) == 0 {
		t.Fatalf("%+v", ee)
	}
}

func TestParseEvent_UnknownMethodFallback(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "some/future/event",
		Params: json.RawMessage(`{"foo":"bar"}`),
	}
	ev, err := ParseEvent(n)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := ev.(*types.UnknownEvent)
	if !ok {
		t.Fatalf("got %T, want *UnknownEvent", ev)
	}
	if u.Method != "some/future/event" || u.EventMethod() != "some/future/event" {
		t.Fatalf("Method = %q", u.Method)
	}
	if string(u.Params) != `{"foo":"bar"}` {
		t.Fatalf("Params not preserved: %q", u.Params)
	}
}

func TestParseEvent_ItemStartedMissingItemIsError(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","itemId":"I"}`),
	}
	_, err := ParseEvent(n)
	if err == nil {
		t.Fatal("expected error for missing item field")
	}
	if !types.IsMessageParseError(err) {
		t.Fatalf("expected MessageParseError, got %T", err)
	}
}

func TestParseEvent_ItemUpdatedMissingDeltaIsError(t *testing.T) {
	t.Parallel()
	n := jsonrpc.Notification{
		Method: "item/updated",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U","itemId":"I"}`),
	}
	_, err := ParseEvent(n)
	if err == nil {
		t.Fatal("expected error for missing delta field")
	}
}

func TestParseEvent_EmptyParamsIsOK(t *testing.T) {
	t.Parallel()
	// For thread/started with empty params we still get a valid event (no
	// thread ID) — the spec is permissive for notifications without a
	// payload.
	n := jsonrpc.Notification{Method: "thread/started", Params: nil}
	ev, err := ParseEvent(n)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ev.(*types.ThreadStarted); !ok {
		t.Fatalf("got %T", ev)
	}
}

func TestParseEvent_ItemEnvelopeFallbackToInnerID(t *testing.T) {
	t.Parallel()
	// When the outer envelope has no itemId but the inner item has .id, the
	// parser uses the inner id.
	n := jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"T","turnId":"U",` +
			`"item":{"id":"inner-id","type":"agentMessage","text":"X"}}`),
	}
	ev, _ := ParseEvent(n)
	is := ev.(*types.ItemStarted)
	if is.ItemID != "inner-id" {
		t.Fatalf("ItemID = %q, want inner-id", is.ItemID)
	}
}
