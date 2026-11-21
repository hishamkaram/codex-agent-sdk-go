package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestParseItem_SubAgentActivityModelMetadata(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		fields string
		want   bool
	}{
		{name: "resolved", fields: `,"model":"child-model","reasoningEffort":"high"`, want: true},
		{name: "null", fields: `,"model":null,"reasoningEffort":null`},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(`{"type":"subAgentActivity","id":"activity","agentPath":"agents/reviewer","agentThreadId":"child","kind":"started"` + tc.fields + `}`)
			item, err := ParseItem(raw)
			if err != nil {
				t.Fatalf("ParseItem: %v", err)
			}
			activity, ok := item.(*types.SubAgentActivity)
			if !ok {
				t.Fatalf("item = %T, want SubAgentActivity", item)
			}
			if !tc.want {
				if activity.Model != nil || activity.ReasoningEffort != nil {
					t.Fatalf("model/effort = %v/%v, want nil", activity.Model, activity.ReasoningEffort)
				}
				return
			}
			if activity.Model == nil || *activity.Model != "child-model" ||
				activity.ReasoningEffort == nil || *activity.ReasoningEffort != "high" {
				t.Fatalf("activity = %+v, want child-model and high effort", activity)
			}
		})
	}
}

func TestParseItem_CollabAgentToolCallModelMetadata(t *testing.T) {
	t.Parallel()

	// Preserve the existing string API when optional wire values are null or absent.
	for _, tc := range []struct {
		name   string
		fields string
		model  string
		effort string
	}{
		{name: "requested", fields: `,"model":"child-model","reasoningEffort":"high"`, model: "child-model", effort: "high"},
		{name: "null", fields: `,"model":null,"reasoningEffort":null`},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(`{"type":"collabAgentToolCall","id":"call","tool":"spawnAgent","status":"completed","agentsStates":{},"senderThreadId":"parent","receiverThreadIds":["child"]` + tc.fields + `}`)
			item, err := ParseItem(raw)
			if err != nil {
				t.Fatalf("ParseItem: %v", err)
			}
			call, ok := item.(*types.CollabAgentToolCall)
			if !ok || call.Model != tc.model || call.ReasoningEffort != tc.effort {
				t.Fatalf("item = %#v, want collab model %q and effort %q", item, tc.model, tc.effort)
			}
		})
	}
}

func TestParseMCPEnvironmentMetadata(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		field         string
		environmentID string
	}{
		{name: "first environment", field: `,"environmentId":"env-one"`, environmentID: "env-one"},
		{name: "second environment", field: `,"environmentId":"env-two"`, environmentID: "env-two"},
		{name: "null", field: `,"environmentId":null`},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item, err := ParseItem(json.RawMessage(`{"type":"mcpToolCall","id":"call","server":"shared","tool":"inspect","status":"completed","arguments":{}` + tc.field + `}`))
			if err != nil {
				t.Fatalf("ParseItem: %v", err)
			}
			call, ok := item.(*types.MCPToolCall)
			if !ok || call.ServerName != "shared" {
				t.Fatalf("item = %#v, want MCP call for shared server", item)
			}

			ev, err := ParseEvent(jsonrpc.Notification{
				Method: "mcpServer/startupStatus/updated",
				Params: json.RawMessage(`{"name":"shared","status":"ready","threadId":"child"` + tc.field + `}`),
			})
			if err != nil {
				t.Fatalf("ParseEvent: %v", err)
			}
			status, ok := ev.(*types.MCPServerStartupStatusUpdated)
			if !ok || status.Name != "shared" {
				t.Fatalf("event = %#v, want MCP startup event for shared server", ev)
			}
			for source, id := range map[string]*string{"tool call": call.EnvironmentID, "startup event": status.EnvironmentID} {
				switch {
				case tc.environmentID == "" && id != nil:
					t.Errorf("%s environment ID = %q, want nil", source, *id)
				case tc.environmentID != "" && (id == nil || *id != tc.environmentID):
					t.Errorf("%s environment ID = %v, want %q", source, id, tc.environmentID)
				}
			}
		})
	}
}

func TestParseItem_FunctionCallOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		output    string
		namespace string
		field     string
	}{
		{name: "text", output: `"plain result"`},
		{name: "content", output: `[ {"type":"input_text","text":"structured result"} ]`, namespace: "tools", field: `,"namespace":"tools"`},
		{name: "null namespace", output: `[]`, field: `,"namespace":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(`{"type":"functionCallOutput","id":"output-1","name":"inspect","output":` + tc.output + tc.field + `}`)
			item, err := ParseItem(raw)
			if err != nil {
				t.Fatalf("ParseItem: %v", err)
			}
			output, ok := item.(*types.FunctionCallOutput)
			if !ok || output.ID != "output-1" || output.Name != "inspect" || string(output.Output) != tc.output {
				t.Fatalf("item = %#v, want function output with raw body %s", item, tc.output)
			}
			if tc.namespace == "" && output.Namespace != nil ||
				tc.namespace != "" && (output.Namespace == nil || *output.Namespace != tc.namespace) {
				t.Fatalf("namespace = %v, want %q", output.Namespace, tc.namespace)
			}
		})
	}
}
