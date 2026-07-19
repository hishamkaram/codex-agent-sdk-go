package types

import (
	"encoding/json"
	"testing"
)

func TestFileChangePartUnmarshalPreservesOmittedFields(t *testing.T) {
	t.Parallel()

	part := FileChangePart{Path: "/tmp/original.txt", Operation: "create", Diff: "old"}
	if err := json.Unmarshal([]byte(`{"diff":"new"}`), &part); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if part.Path != "/tmp/original.txt" || part.Operation != "create" || part.Diff != "new" {
		t.Fatalf("partial unmarshal cleared omitted fields: %#v", part)
	}
}

func TestFileChangePartUnmarshalRefreshesKindProjection(t *testing.T) {
	t.Parallel()

	part := FileChangePart{Operation: "create"}
	if err := json.Unmarshal([]byte(`{"kind":{"type":"update"}}`), &part); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if part.Kind == nil || part.Kind.Type != "update" || part.Operation != "modify" {
		t.Fatalf("current kind did not refresh compatibility projection: %#v", part)
	}
}

func TestFileChangePartUnmarshalNullKindClearsProjection(t *testing.T) {
	t.Parallel()

	part := FileChangePart{Kind: &PatchChangeKind{Type: "add"}, Operation: "create"}
	if err := json.Unmarshal([]byte(`{"kind":null}`), &part); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if part.Kind != nil || part.Operation != "" {
		t.Fatalf("null kind left a stale compatibility projection: %#v", part)
	}
}
