package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// spawnedProc bundles the started subprocess and its stdio pipes so spawnWithRetry
// can return them without a long unnamed result list. The pipes are owned by the
// caller once spawnWithRetry succeeds.
type spawnedProc struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	childStdin *os.File
	stdout     io.ReadCloser
	stderr     io.ReadCloser
}

// logCLIVersion performs the soft version probe: it never fails the connection,
// logging a warning when the probe fails or the codex CLI is below
// RecommendedCLIVersion, and a debug line when the version is acceptable.
func (t *AppServer) logCLIVersion(ctx context.Context, cli CLI) {
	probe := t.cfg.VersionProbe
	if probe == nil {
		version, err := cli.ProbeVersion(ctx)
		probe = &CLIVersionProbeResult{Version: version, Err: err}
	}
	if probe.Err != nil {
		t.logger.Warn("could not probe codex CLI version (continuing)",
			zap.String("cli", cli.path), zap.Error(probe.Err))
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
func (t *AppServer) spawnWithRetry(ctx context.Context, cli CLI, args []string) (*spawnedProc, error) {
	var proc *spawnedProc
	var pipeErr error
	startErr := retryOnETXTBSY(ctx, func() error {
		candidate, err := newSpawnedProc(cli.newSpawnCommand(args))
		if err != nil {
			pipeErr = err
			return err
		}
		if err := candidate.start(); err != nil {
			candidate.closePipes()
			return err
		}
		proc = candidate
		return nil
	}, func(attempt int) {
		t.logger.Debug("spawn hit ETXTBSY; retrying",
			zap.Int("attempt", attempt), zap.String("cli", cli.path))
	})
	if pipeErr != nil {
		return nil, pipeErr
	}
	if startErr != nil {
		return nil, types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cli.path), startErr)
	}
	return proc, nil
}

func (cli CLI) newSpawnCommand(args []string) *exec.Cmd {
	// The subprocess lifetime is owned by AppServer.Close, not the connect
	// attempt. Client.Connect cancels its context after the handshake.
	cmd := exec.Command(cli.path, args...)
	cli.configure(cmd)
	return cmd
}

func newSpawnedProc(cmd *exec.Cmd) (*spawnedProc, error) {
	stdin, childStdin, err := openProcessStdin(cmd)
	if err != nil {
		return nil, types.NewCLIConnectionError("stdin pipe", err)
	}
	proc := &spawnedProc{cmd: cmd, stdin: stdin, childStdin: childStdin}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		proc.closePipes()
		return nil, types.NewCLIConnectionError("stdout pipe", err)
	}
	proc.stdout = stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		proc.closePipes()
		return nil, types.NewCLIConnectionError("stderr pipe", err)
	}
	proc.stderr = stderr
	return proc, nil
}

func (p *spawnedProc) start() error {
	err := p.cmd.Start()
	p.closeChildStdin()
	return err
}

func (p *spawnedProc) closeChildStdin() {
	if p.childStdin != nil {
		_ = p.childStdin.Close()
		p.childStdin = nil
	}
}

func (p *spawnedProc) closePipes() {
	_ = p.stdin.Close()
	p.closeChildStdin()
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	if p.stderr != nil {
		_ = p.stderr.Close()
	}
}
