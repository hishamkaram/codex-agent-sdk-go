package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestParseEvent_AgentMessageDelta_FromRealWireShape(t *testing.T) {
	t.Parallel()
	// Real wire payload captured in the spike transcript. The top-level
	// "delta" is a plain string — NOT a structured {"type":"...","text_chunk":"..."}.
	raw := `{"threadId":"T1","turnId":"U1","itemId":"msg_abc","delta":"OK"}`
	ev, err := ParseEvent(jsonrpc.Notification{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	iu, ok := ev.(*types.ItemUpdated)
	if !ok {
		t.Fatalf("got %T, want *ItemUpdated", ev)
	}
	if iu.ThreadID != "T1" || iu.TurnID != "U1" || iu.ItemID != "msg_abc" {
		t.Fatalf("ids: %+v", iu)
	}
	d, ok := iu.Delta.(*types.AgentMessageDelta)
	if !ok {
		t.Fatalf("delta type: %T", iu.Delta)
	}
	if d.TextChunk != "OK" {
		t.Fatalf("TextChunk = %q", d.TextChunk)
	}
	if d.DeltaType() != "agent_message_delta" {
		t.Fatalf("DeltaType = %q", d.DeltaType())
	}
}

func TestParseEvent_AgentMessageDelta_EmptyDelta(t *testing.T) {
	t.Parallel()
	raw := `{"threadId":"T","turnId":"U","itemId":"I","delta":""}`
	ev, err := ParseEvent(jsonrpc.Notification{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	iu := ev.(*types.ItemUpdated)
	d := iu.Delta.(*types.AgentMessageDelta)
	if d.TextChunk != "" {
		t.Fatalf("TextChunk = %q", d.TextChunk)
	}
}
