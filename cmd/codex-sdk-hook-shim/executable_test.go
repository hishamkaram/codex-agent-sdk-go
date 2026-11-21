package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestShimExecutable exercises the actual command and reports its separate
// subprocess coverage with go test -v -run '^TestShimExecutable$'.
func TestShimExecutable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("native shim subprocess and coverage audit uses Linux")
	}

	binaryPath := filepath.Join(t.TempDir(), "hook-shim")
	buildCtx, cancelBuild := context.WithTimeout(t.Context(), time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-cover", "-covermode=atomic", "-o", binaryPath, ".")
	build.Env = append(build.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build instrumented hook shim: %v\n%s", err, out)
	}

	const payload = `{"hook_event_name":"PreToolUse","tool_input":{"command":"pwd"}}`
	coverageDir := t.TempDir()
	for _, tc := range []struct {
		name       string
		socket     string
		badStdin   bool
		diagnostic string
	}{
		{name: "unset socket"},
		{name: "missing socket", socket: filepath.Join(t.TempDir(), "missing.sock"), diagnostic: "bridge call failed:"},
		{name: "unreadable stdin", badStdin: true, diagnostic: "read stdin:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := newShimChild(t, binaryPath, coverageDir, tc.socket, payload)
			if tc.badStdin {
				badInput, err := os.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = badInput.Close() })
				child.cmd.Stdin = badInput
			}
			child.start(t)
			child.wait(t, 0)
			if child.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want fail-open silence", child.stdout.String())
			}
			stderr := child.stderr.String()
			if (tc.diagnostic == "" && stderr != "") || !strings.Contains(stderr, tc.diagnostic) {
				t.Errorf("stderr = %q, want diagnostic %q", stderr, tc.diagnostic)
			}
		})
	}

	t.Run("forwards the bridge decision", func(t *testing.T) {
		listener := newShimTestListener(t)
		child := newShimChild(t, binaryPath, coverageDir, listener.Addr().String(), payload)
		child.start(t)

		const output = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`
		const reply = `{"stdout":"{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\"}}","stderr":"fixture decision\n","exit_code":2}`
		serveShimFixture(t, listener, payload, reply)
		child.wait(t, 2)
		if child.stdout.String() != output || child.stderr.String() != "fixture decision\n" {
			t.Errorf("forwarded stdout/stderr = %q / %q", child.stdout.String(), child.stderr.String())
		}
	})
	t.Run("invalid bridge reply falls through", func(t *testing.T) {
		listener := newShimTestListener(t)
		child := newShimChild(t, binaryPath, coverageDir, listener.Addr().String(), payload)
		child.start(t)
		serveShimFixture(t, listener, payload, `not JSON`)
		child.wait(t, 0)
		if child.stdout.Len() != 0 || !strings.Contains(child.stderr.String(), "unmarshal response:") {
			t.Errorf("invalid reply stdout/stderr = %q / %q", child.stdout.String(), child.stderr.String())
		}
	})

	profile := filepath.Join(t.TempDir(), "shim.out")
	reportCtx, cancelReport := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelReport()
	if out, err := exec.CommandContext(reportCtx, "go", "tool", "covdata", "textfmt", "-i="+coverageDir, "-o="+profile).CombinedOutput(); err != nil {
		t.Fatalf("collect shim executable coverage: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(reportCtx, "go", "tool", "cover", "-func="+profile).CombinedOutput()
	if err != nil {
		t.Fatalf("report shim executable coverage: %v\n%s", err, out)
	}
	t.Logf("Shim subprocess coverage\n%s", out)
}

type shimChild struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newShimChild(t *testing.T, binaryPath, coverageDir, socket, payload string) *shimChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	t.Cleanup(cancel)
	child := &shimChild{cmd: exec.CommandContext(ctx, binaryPath)}
	child.cmd.Stdin = strings.NewReader(payload)
	child.cmd.Stdout, child.cmd.Stderr = &child.stdout, &child.stderr
	child.cmd.Env = []string{
		"CODEX_SDK_HOOK_SOCKET=" + socket,
		"CODEX_SDK_HOOK_REQUEST_ID=fixture-id",
		"SHIM_PRIVATE_ENV=never-forward",
		"GOCOVERDIR=" + coverageDir,
	}
	return child
}

func (child *shimChild) start(t *testing.T) {
	t.Helper()
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.cmd.ProcessState == nil {
			_ = child.cmd.Process.Kill()
			_ = child.cmd.Wait()
		}
	})
}

func (child *shimChild) wait(t *testing.T, code int) {
	t.Helper()
	err := child.cmd.Wait()
	if child.cmd.ProcessState.ExitCode() != code || (code == 0 && err != nil) {
		t.Fatalf("shim exit = %v, want %d, stderr = %q", err, code, child.stderr.String())
	}
}

func newShimTestListener(t *testing.T) *net.UnixListener {
	t.Helper()
	dir, err := os.MkdirTemp("", "cx-shim-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return listener
}

func serveShimFixture(t *testing.T, listener *net.UnixListener, payload, response string) {
	t.Helper()
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		t.Fatalf("read request size: %v", err)
	}
	size := binary.BigEndian.Uint32(length[:])
	if size > 1024 {
		t.Fatalf("fixture got request size %d, want at most 1024", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatalf("read request body: %v", err)
	}

	var request struct {
		Version string            `json:"shim_version"`
		Payload string            `json:"stdin"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Version != "0.2.0" || request.Payload != payload || len(request.Env) != 1 ||
		request.Env["CODEX_SDK_HOOK_REQUEST_ID"] != "fixture-id" {
		t.Fatalf("request = %+v, want payload and request ID only", request)
	}

	frame := binary.BigEndian.AppendUint32(nil, uint32(len(response)))
	if _, err := conn.Write(append(frame, response...)); err != nil {
		t.Fatalf("write fixture response: %v", err)
	}
}
