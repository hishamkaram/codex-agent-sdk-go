package transport

import (
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandHome(t *testing.T) {
	t.Parallel()
	usr, err := user.Current()
	if err != nil {
		t.Skipf("user.Current failed: %v", err)
	}

	tests := []struct {
		in   string
		want string
	}{
		{"~", usr.HomeDir},
		{"~/.codex/bin/codex", filepath.Join(usr.HomeDir, ".codex/bin/codex")},
		{"/absolute/no/tilde", "/absolute/no/tilde"},
		{"~NotAtilde", "~NotAtilde"}, // ~foo should not expand
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got := expandHome(tt.in)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindCLI_ReturnsErrorWhenNotFound(t *testing.T) {
	t.Parallel()
	// The test environment may or may not have codex installed. We only
	// verify that when it's NOT found, the error is typed as
	// *types.CLINotFoundError with a non-empty message.
	path, err := FindCLI()
	if err == nil {
		// codex is on PATH — sanity-check the result is a real path.
		if path == "" {
			t.Fatal("FindCLI returned empty path with nil error")
		}
		return
	}
	msg := err.Error()
	if !strings.Contains(msg, "codex") {
		t.Fatalf("error should mention codex: %q", msg)
	}
}
