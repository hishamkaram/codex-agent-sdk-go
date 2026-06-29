package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// spawnedProc bundles the started subprocess and its stdio pipes so spawnWithRetry
// can return them without a long unnamed result list. The pipes are owned by the
// caller once spawnWithRetry succeeds.
type spawnedProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// logCLIVersion performs the soft version probe: it never fails the connection,
// logging a warning when the probe fails or the codex CLI is below
// RecommendedCLIVersion, and a debug line when the version is acceptable.
func (t *AppServer) logCLIVersion(ctx context.Context, cliPath string) {
	v, err := probeCLIVersionCtx(ctx, cliPath)
	if err != nil {
		t.logger.Warn("could not probe codex CLI version (continuing)",
			zap.String("cli", cliPath), zap.Error(err))
		return
	}
	recommended, _ := ParseSemVer(RecommendedCLIVersion)
	if !v.AtLeast(recommended) {
		t.logger.Warn("codex CLI version below recommended",
			zap.String("found", v.String()),
			zap.String("recommended", RecommendedCLIVersion))
		return
	}
	t.logger.Debug("codex CLI version ok", zap.String("version", v.String()))
}

// spawnWithRetry builds and starts the codex app-server subprocess, returning
// its stdio pipes on success. It retries on ETXTBSY ("text file busy"): under
// heavy concurrent fork/exec, the child of an unrelated spawn can transiently
// hold a write fd to the target binary in the window between fork and exec,
// which the kernel reports as ETXTBSY. cmd.Start() consumes its pipes, so the
// command is rebuilt each attempt. This is the standard mitigation for the Go
// fork/exec ETXTBSY race. The returned pipes are owned by the caller.
func (t *AppServer) spawnWithRetry(ctx context.Context, cliPath string, args []string) (*spawnedProc, error) {
	const maxSpawnAttempts = 5
	for attempt := 1; ; attempt++ {
		cmd := exec.CommandContext(ctx, cliPath, args...)
		cmd.Env = buildEnv(t.cfg.Env)

		stdin, pipeErr := cmd.StdinPipe()
		if pipeErr != nil {
			return nil, types.NewCLIConnectionError("stdin pipe", pipeErr)
		}
		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			_ = stdin.Close()
			return nil, types.NewCLIConnectionError("stdout pipe", pipeErr)
		}
		stderr, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, types.NewCLIConnectionError("stderr pipe", pipeErr)
		}

		startErr := cmd.Start()
		if startErr == nil {
			return &spawnedProc{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
		}
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if errors.Is(startErr, syscall.ETXTBSY) && attempt < maxSpawnAttempts {
			t.logger.Debug("spawn hit ETXTBSY; retrying",
				zap.Int("attempt", attempt), zap.String("cli", cliPath))
			select {
			case <-ctx.Done():
				return nil, types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cliPath), ctx.Err())
			case <-time.After(time.Duration(attempt) * 5 * time.Millisecond):
			}
			continue
		}
		return nil, types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cliPath), startErr)
	}
}

// buildEnv overlays keyEqVals on os.Environ. An entry "KEY=" unsets KEY.
func buildEnv(keyEqVals []string) []string {
	if len(keyEqVals) == 0 {
		return os.Environ()
	}
	out := make([]string, 0, len(os.Environ())+len(keyEqVals))
	// Index existing entries by key for fast override.
	overrides := make(map[string]string, len(keyEqVals))
	for _, kv := range keyEqVals {
		k, v, _ := splitKV(kv)
		overrides[k] = v
	}
	for _, kv := range os.Environ() {
		k, _, _ := splitKV(kv)
		if _, overridden := overrides[k]; overridden {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		if v == "" {
			continue // unset
		}
		out = append(out, k+"="+v)
	}
	return out
}

// splitKV splits "KEY=VALUE" at the FIRST '=' — values may contain '='.
func splitKV(kv string) (key, value string, ok bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}
