package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
)

func TestLiveCLITargetAcceptsCodexLogin(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		useCodexHome bool
	}{
		{name: "default home"},
		{name: "CODEX_HOME", useCodexHome: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			marker, output, err := runLiveCLITargetWithLogin(t, tc.useCodexHome)
			if err != nil {
				t.Fatalf("make test-codex-livecli rejected codex login: %v\n%s", err, output)
			}
			assertLiveCLITargetInvoked(t, marker)
		})
	}
}

func TestLiveCLITargetRejectsMissingAuthentication(t *testing.T) {
	t.Parallel()

	marker, output, err := runLiveCLITargetWithoutLogin(t)
	if err == nil {
		t.Fatalf("make test-codex-livecli unexpectedly passed; output=%s", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("live test command ran without authentication: %v", statErr)
	}
	if !strings.Contains(string(output), "Codex authentication required") {
		t.Fatalf("authentication output = %q, want actionable failure", output)
	}
}

func runLiveCLITargetWithLogin(t *testing.T, useCodexHome bool) (string, []byte, error) {
	return runLiveCLITarget(t, useCodexHome, true)
}

func runLiveCLITargetWithoutLogin(t *testing.T) (string, []byte, error) {
	return runLiveCLITarget(t, false, false)
}

func runLiveCLITarget(t *testing.T, useCodexHome, withLogin bool) (string, []byte, error) {
	t.Helper()
	home := t.TempDir()
	authDir := filepath.Join(home, ".codex")
	codexHome := ""
	if useCodexHome {
		authDir = t.TempDir()
		codexHome = authDir
	}
	if withLogin {
		if err := os.MkdirAll(authDir, 0o700); err != nil {
			t.Fatalf("Mkdir auth dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte("{}"), 0o600); err != nil {
			t.Fatalf("WriteFile auth: %v", err)
		}
	}

	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "go-invoked")
	fakeGo := []byte("#!/bin/sh\nprintf invoked > \"$LIVE_AUTH_GO_MARKER\"\n")
	if err := os.WriteFile(filepath.Join(binDir, "go"), fakeGo, 0o700); err != nil {
		t.Fatalf("WriteFile fake go: %v", err)
	}

	cmd := exec.Command("make", "test-codex-livecli")
	cmd.Env = transport.BuildRuntimeEnvironment([]string{
		"HOME=" + home,
		"CODEX_HOME=" + codexHome,
		"OPENAI_API_KEY=",
		"LIVE_AUTH_GO_MARKER=" + marker,
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	output, err := cmd.CombinedOutput()
	return marker, output, err
}

func assertLiveCLITargetInvoked(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("live test command was not invoked: %v", err)
	}
}
