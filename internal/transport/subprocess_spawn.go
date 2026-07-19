package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	probe := t.cfg.VersionProbe
	if probe == nil {
		version, err := probeCLIVersionCtx(ctx, cliPath)
		probe = &CLIVersionProbeResult{Version: version, Err: err}
	}
	if probe.Err != nil {
		t.logger.Warn("could not probe codex CLI version (continuing)",
			zap.String("cli", cliPath), zap.Error(probe.Err))
		return
	}
	recommended, _ := ParseSemVer(RecommendedCLIVersion)
	if !probe.Version.AtLeast(recommended) {
		t.logger.Warn("codex CLI version below recommended",
			zap.String("found", probe.Version.String()),
			zap.String("recommended", RecommendedCLIVersion))
		return
	}
	t.logger.Debug("codex CLI version ok", zap.String("version", probe.Version.String()))
}

// spawnWithRetry builds and starts the codex app-server subprocess, returning
// its stdio pipes on success. It retries on ETXTBSY ("text file busy"): under
// heavy concurrent fork/exec, the child of an unrelated spawn can transiently
// hold a write fd to the target binary in the window between fork and exec,
// which the kernel reports as ETXTBSY. cmd.Start() consumes its pipes, so the
// command is rebuilt each attempt. This is the standard mitigation for the Go
// fork/exec ETXTBSY race. The returned pipes are owned by the caller.
func (t *AppServer) spawnWithRetry(ctx context.Context, cliPath string, args []string) (*spawnedProc, error) {
	resolvedCLIPath, err := resolveExplicitCLIPath(cliPath)
	if err != nil {
		return nil, types.NewCLIConnectionError(fmt.Sprintf("resolve %q", cliPath), err)
	}

	var proc *spawnedProc
	var pipeErr error
	startErr := retryOnETXTBSY(ctx, func() error {
		candidate, err := newSpawnedProc(t.newSpawnCommand(resolvedCLIPath, args))
		if err != nil {
			pipeErr = err
			return err
		}
		if err := candidate.cmd.Start(); err != nil {
			candidate.closePipes()
			return err
		}
		proc = candidate
		return nil
	}, func(attempt int) {
		t.logger.Debug("spawn hit ETXTBSY; retrying",
			zap.Int("attempt", attempt), zap.String("cli", cliPath))
	})
	if pipeErr != nil {
		return nil, pipeErr
	}
	if startErr != nil {
		return nil, types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cliPath), startErr)
	}
	return proc, nil
}

// resolveExplicitCLIPath preserves PATH lookup for a bare CLI name while
// anchoring an explicitly relative path before exec.Cmd applies its Dir.
func resolveExplicitCLIPath(cliPath string) (string, error) {
	hasExplicitPath := strings.ContainsRune(cliPath, '/') || strings.ContainsRune(cliPath, filepath.Separator)
	if filepath.IsAbs(cliPath) || !hasExplicitPath {
		return cliPath, nil
	}
	resolved, err := filepath.Abs(cliPath)
	if err != nil {
		return "", fmt.Errorf("resolve explicit relative CLI path %q: %w", cliPath, err)
	}
	return resolved, nil
}

func (t *AppServer) newSpawnCommand(cliPath string, args []string) *exec.Cmd {
	// The subprocess lifetime is owned by AppServer.Close, not by the
	// connect-attempt context. Client.Connect cancels that context after a
	// successful handshake; tying exec.Cmd to it kills the live app-server.
	cmd := exec.Command(cliPath, args...)
	if t.cfg.Cwd != "" {
		cmd.Dir = t.cfg.Cwd
	}
	cmd.Env = BuildRuntimeEnvironment(t.cfg.Env)
	return cmd
}

func newSpawnedProc(cmd *exec.Cmd) (*spawnedProc, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, types.NewCLIConnectionError("stdin pipe", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, types.NewCLIConnectionError("stdout pipe", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, types.NewCLIConnectionError("stderr pipe", err)
	}
	return &spawnedProc{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (p *spawnedProc) closePipes() {
	_ = p.stdin.Close()
	_ = p.stdout.Close()
	_ = p.stderr.Close()
}

// BuildRuntimeEnvironment overlays keyEqVals on os.Environ. An entry "KEY="
// unsets KEY. CLI probes and the app-server subprocess must use this same
// environment contract.
func BuildRuntimeEnvironment(keyEqVals []string) []string {
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
