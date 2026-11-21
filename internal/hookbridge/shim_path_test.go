package hookbridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGeneratedHookInvokesShimWithShellSpecialCharactersInPath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command contract")
	}
	shim := filepath.Join(t.TempDir(), "user's hook shim")
	script := "#!/bin/sh\nprintf '%s\\n' 'shim invoked'\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	data, err := GenerateHooksJSON(shim, 30)
	if err != nil {
		t.Fatal(err)
	}
	var config HooksConfig
	if decodeErr := json.Unmarshal(data, &config); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	command := config.Hooks["PreToolUse"][0].Hooks[0].Command
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/sh", "-c", command).CombinedOutput()
	if err != nil || string(out) != "shim invoked\n" {
		t.Fatalf("run generated hook for %q = %q, %v, want shim output", shim, out, err)
	}
}
