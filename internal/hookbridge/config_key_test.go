package hookbridge

import (
	"testing"
)

func TestClientCommands_HookConfigKeyDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{"hooks", true},
		{"hooks.PreToolUse", true},
		{"hooks.pre_tool_use.0", true},
		{"model", false},
		{"features.hooks", false},
		{"features.codex_hooks", false},
		{"hooks_json", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			if got := IsHooksConfigKeyPath(tt.key); got != tt.want {
				t.Fatalf("IsHooksConfigKeyPath(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
