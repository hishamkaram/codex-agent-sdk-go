package types

import (
	"testing"
)

// TestMCPToolCallErrorField_UnmarshalJSON pins the forward-compat posture of
// the helper type: accept the codex 0.121.0 object shape `{"message":"..."}`,
// the legacy bare-string shape, null, and any unknown shape — never return
// an error. US1-AC2/AC3/AC4 acceptance scenarios.
func TestMCPToolCallErrorField_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantMessage string
	}{
		{
			name:        "object with message",
			input:       `{"message":"not supported"}`,
			wantMessage: "not supported",
		},
		{
			name:        "bare string",
			input:       `"legacy string format"`,
			wantMessage: "legacy string format",
		},
		{
			name:        "null",
			input:       `null`,
			wantMessage: "",
		},
		{
			name:        "unknown shape array",
			input:       `[1,2,3]`,
			wantMessage: "",
		},
		{
			name:        "empty object",
			input:       `{}`,
			wantMessage: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got MCPToolCallErrorField
			if err := got.UnmarshalJSON([]byte(tt.input)); err != nil {
				t.Fatalf("UnmarshalJSON(%q) returned error: %v", tt.input, err)
			}
			if got.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", got.Message, tt.wantMessage)
			}
		})
	}
}

// FuzzMCPToolCallError proves the forward-compat posture: no matter what
// bytes arrive (any arbitrary JSON-ish or non-JSON payload), UnmarshalJSON
// never returns a non-nil error. Seeded with the canonical shapes plus
// edge values.
func FuzzMCPToolCallError(f *testing.F) {
	// Seed corpus — the five shapes we care about plus an integer value to
	// exercise the unknown-shape branch.
	f.Add([]byte(`{"message":"x"}`))
	f.Add([]byte(`"s"`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`42`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var got MCPToolCallErrorField
		if err := got.UnmarshalJSON(data); err != nil {
			t.Errorf("UnmarshalJSON(%q) returned error %v; helper must be forward-compat (never error)", data, err)
		}
	})
}
