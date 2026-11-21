package transport

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProbeCLIVersionRequestsOnlyVersion(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell executable")
	}

	path := filepath.Join(t.TempDir(), "codex")
	writeExecutableFixture(t, path, `#!/bin/sh
if [ "$#" -ne 1 ] || [ "$1" != "--version" ]; then
  exit 23
fi
printf '%s\n' 'codex-cli 0.171.0-test'
`)

	got, err := ProbeCLIVersion(path)
	if want := (SemVer{Major: 0, Minor: 171, Patch: 0}); err != nil || got != want {
		t.Fatalf("ProbeCLIVersion() = %+v, %v, want %+v", got, err, want)
	}
}

func TestEnvironmentHasKeyRecognizesWindowsDriveEntries(t *testing.T) {
	t.Parallel()
	entries := []string{`=C:=C:\work`, "SystemRoot=", "PATH=SYSTEMROOT", "invalid"}
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{key: "=c:", want: true},
		{key: "SYSTEMROOT", want: true},
		{key: "invalid"},
		{key: "missing"},
	} {
		if got := environmentHasKey(entries, tc.key); got != tc.want {
			t.Errorf("environmentHasKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestSpawnedProcStartFailureReleasesPipes(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing-executable.exe")
	cmd := exec.Command(missing)
	proc, err := newSpawnedProc(cmd)
	if err != nil {
		t.Fatalf("configure stdio pipes: %v", err)
	}
	t.Cleanup(proc.closePipes)
	if err := proc.start(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("start missing executable = %v, want missing-file error", err)
	}
	proc.closePipes()
	if cmd.Process != nil {
		t.Fatal("failed start left a process handle")
	}
	if _, err := proc.stdin.Write([]byte("unused")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("write after cleanup = %v, want closed stdin", err)
	}
	for name, pipe := range map[string]io.ReadCloser{"stdout": proc.stdout, "stderr": proc.stderr} {
		var oneByte [1]byte
		if _, err := pipe.Read(oneByte[:]); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s after cleanup = %v, want closed pipe", name, err)
		}
	}
}

func TestWarmPoolRemainsEmptyAfterRelease(t *testing.T) {
	t.Parallel()
	pool := NewWarmPool()
	if got := pool.Acquire(t.Context()); got != nil {
		t.Fatal("new warm pool returned a transport")
	}
	pool.Release(&AppServer{})
	if got := pool.Acquire(t.Context()); got != nil {
		t.Fatal("zero-capacity warm pool retained a transport")
	}
}

func TestAppServerConnectKeepsFirstLaunchFailure(t *testing.T) {
	t.Parallel()
	app := NewAppServer(AppServerConfig{CLIPath: filepath.Join(t.TempDir(), "no-codex.exe")})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	firstErr := app.Connect(ctx)
	if !errors.Is(firstErr, os.ErrNotExist) {
		t.Fatalf("initial launch = %v, want missing-file error", firstErr)
	}
	const readers = 4
	results := make(chan error, readers)
	for range readers {
		go func() { results <- app.Connect(ctx) }()
	}
	for range readers {
		select {
		case err := <-results:
			if !errors.Is(err, firstErr) || !errors.Is(err, os.ErrNotExist) {
				t.Errorf("repeated Connect returned %v, want original launch error %v", err, firstErr)
			}
		case <-ctx.Done():
			t.Fatal("repeated Connect did not finish before the test deadline")
		}
	}
	if app.Demux() != nil || app.Pid() != 0 {
		t.Fatal("failed launch published transport resources")
	}
}
