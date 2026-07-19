//go:build integration

package tests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSchemaAuthFileUsesCodexHome(t *testing.T) {
	// serial: t.Setenv mutates process-wide state while this test exercises the
	// production environment lookup directly.
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	path, err := schemaAuthFile()
	if err != nil {
		t.Fatalf("schemaAuthFile: %v", err)
	}
	want := filepath.Join(codexHome, "auth.json")
	if path != want {
		t.Fatalf("schemaAuthFile = %q, want %q", path, want)
	}
}

func TestSchemaWorkspaceIsGitRepository(t *testing.T) {
	t.Parallel()

	workspace := schemaWorkspace(t)
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err != nil {
		t.Fatalf("schema workspace git metadata: %v", err)
	}
}

func TestSchemaWorkspaceWriteSandboxClassifiesFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   error
	}{
		{
			name:   "bubblewrap loopback denied",
			script: "#!/bin/sh\necho 'bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted' >&2\nexit 1\n",
			want:   errSchemaWorkspaceWriteBwrapUnavailable,
		},
		{
			name:   "unknown failure",
			script: "#!/bin/sh\necho 'sandbox unavailable' >&2\nexit 1\n",
			want:   errSchemaWorkspaceWriteUnavailable,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := schemaWorkspaceWriteSandboxError(context.Background(), writeSchemaSandboxCLI(t, tt.script))
			if !errors.Is(err, tt.want) {
				t.Fatalf("sandbox preflight error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSchemaWorkspaceWriteSandboxForcesWorkspaceWrite(t *testing.T) {
	// serial: t.Setenv provides the fake CLI's output path.
	argsPath := filepath.Join(t.TempDir(), "sandbox-args")
	t.Setenv("SCHEMA_SANDBOX_ARGS", argsPath)

	script := `#!/bin/sh
printf '%s\n' "$@" > "$SCHEMA_SANDBOX_ARGS"
`
	if err := schemaWorkspaceWriteSandboxError(context.Background(), writeSchemaSandboxCLI(t, script)); err != nil {
		t.Fatalf("sandbox preflight error = %v", err)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read sandbox arguments: %v", err)
	}
	const want = "sandbox\n-c\nsandbox_mode=\"workspace-write\"\n/bin/true\n"
	if got := string(args); got != want {
		t.Fatalf("sandbox arguments = %q, want %q", strings.TrimSpace(got), strings.TrimSpace(want))
	}
}

func TestSchemaWorkspaceWriteSandboxHonorsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	script := "#!/bin/sh\nwhile :; do :; done\n"
	err := schemaWorkspaceWriteSandboxError(ctx, writeSchemaSandboxCLI(t, script))
	if !errors.Is(err, errSchemaWorkspaceWriteUnavailable) {
		t.Fatalf("sandbox preflight error = %v, want sandbox unavailable", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sandbox preflight error = %v, want context deadline exceeded", err)
	}
}

func TestSchemaWorkspaceWriteSandboxBoundsDescendantPipes(t *testing.T) {
	t.Parallel()

	childPID := filepath.Join(t.TempDir(), "sandbox-child.pid")
	t.Cleanup(func() { killSchemaSandboxChild(t, childPID) })
	cli := writeSchemaSandboxCLI(t, fmt.Sprintf(`#!/bin/sh
sleep 30 &
printf '%%s\n' "$!" > %q
wait
`, childPID))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := schemaWorkspaceWriteSandboxError(ctx, cli)
	if !errors.Is(err, errSchemaWorkspaceWriteUnavailable) {
		t.Fatalf("sandbox preflight error = %v, want sandbox unavailable", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sandbox preflight error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("sandbox preflight returned after %s, want bounded descendant-pipe cleanup", elapsed)
	}
	if _, statErr := os.Stat(childPID); statErr != nil {
		t.Fatalf("fake sandbox did not start its descendant: %v", statErr)
	}
}

func killSchemaSandboxChild(t *testing.T, pidFile string) {
	t.Helper()
	rawPID, err := os.ReadFile(pidFile)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Errorf("read sandbox child pid: %v", err)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil {
		t.Errorf("parse sandbox child pid: %v", err)
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Errorf("find sandbox child: %v", err)
		return
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill sandbox child: %v", err)
	}
}

func writeSchemaSandboxCLI(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		t.Fatalf("publish fake codex: %v", err)
	}
	return path
}
