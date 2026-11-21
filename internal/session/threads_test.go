package session

import (
	"encoding/json"
	"testing"
)

func TestExtractThreadModel(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"thread":{"id":"thread-1"},"model":"provider-model"}`)
	if got := extractThreadModel(raw); got != "provider-model" {
		t.Fatalf("extractThreadModel() = %q, want provider-model", got)
	}
}
