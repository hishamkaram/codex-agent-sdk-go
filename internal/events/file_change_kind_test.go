package events

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestParseItemFileChangeCurrentKind(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type":"fileChange",
		"id":"fc-current",
		"status":"completed",
		"changes":[{
			"path":"/tmp/schema-proof.txt",
			"kind":{"type":"update","move_path":"/tmp/schema-proof-old.txt"},
			"diff":"+schema parser proof"
		}]
	}`)
	item, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	change, ok := item.(*types.FileChange)
	if !ok || len(change.Changes) != 1 {
		t.Fatalf("ParseItem returned %#v, want one FileChange part", item)
	}
	part := change.Changes[0]
	if part.Kind == nil || part.Kind.Type != "update" || part.Kind.MovePath == nil {
		t.Fatalf("current kind was not preserved: %#v", part)
	}
	if part.Operation != "modify" {
		t.Fatalf("legacy Operation = %q, want modify", part.Operation)
	}
	encoded, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("marshal FileChangePart: %v", err)
	}
	if strings.Contains(string(encoded), `"operation"`) || !strings.Contains(string(encoded), `"kind"`) {
		t.Fatalf("current FileChangePart remarshal = %s", encoded)
	}
}

func TestParseItemFileChangeLegacyOperation(t *testing.T) {
	t.Parallel()

	item, err := ParseItem(json.RawMessage(`{
		"type":"fileChange",
		"id":"fc-legacy",
		"status":"completed",
		"changes":[{"path":"/tmp/legacy.txt","operation":"create","diff":"+legacy"}]
	}`))
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	change := item.(*types.FileChange)
	if change.Changes[0].Kind != nil || change.Changes[0].Operation != "create" {
		t.Fatalf("legacy operation was not preserved: %#v", change.Changes[0])
	}
}
