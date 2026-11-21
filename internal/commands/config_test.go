package commands

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestWriteConfigBatchCopiesAndNormalizesEdits(t *testing.T) {
	t.Parallel()

	edits := []types.ConfigEntry{
		{KeyPath: "model", Value: "example-model"},
		{KeyPath: "features", MergeStrategy: types.MergeUpsert, Value: true},
	}
	var guarded []string
	guard := func(callerName, keyPath string) error {
		if callerName != "WriteConfigBatch" {
			t.Fatalf("guard caller = %q, want WriteConfigBatch", callerName)
		}
		guarded = append(guarded, keyPath)
		return nil
	}
	send := func(_ context.Context, callerName, method string, params any) (json.RawMessage, error) {
		if !reflect.DeepEqual(guarded, []string{"model", "features"}) {
			t.Fatalf("guarded at send time = %v, want both edit keys", guarded)
		}
		if callerName != "WriteConfigBatch" || method != "config/batchWrite" {
			t.Fatalf("caller/method = %q/%q, want WriteConfigBatch/config/batchWrite", callerName, method)
		}
		wire, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"edits":[{"keyPath":"model","mergeStrategy":"replace","value":"example-model"},{"keyPath":"features","mergeStrategy":"upsert","value":true}]}`
		var gotObject, wantObject map[string]any
		if err := json.Unmarshal(wire, &gotObject); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want), &wantObject); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotObject, wantObject) {
			t.Fatalf("params = %s, want %s", wire, want)
		}
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	response, err := WriteConfigBatch(t.Context(), send, edits, guard)
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.Status != "ok" || edits[0].MergeStrategy != "" || edits[1].MergeStrategy != types.MergeUpsert {
		t.Fatalf("response = %+v, caller edits = %+v, want success and unmodified edits", response, edits)
	}
}
