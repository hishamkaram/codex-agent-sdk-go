package transport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	maxCLICommandAttempts       = 5
	cliCommandRetryBaseInterval = 5 * time.Millisecond
)

// RunCLICommand runs a short-lived Codex CLI command with the SDK environment
// overlay. It retries only an ETXTBSY process-start failure, recreating the
// exec.Cmd each time because failed commands cannot be reused.
func RunCLICommand(
	ctx context.Context,
	cliPath string,
	env []string,
	waitDelay time.Duration,
	args ...string,
) (stdout, stderr string, err error) {
	cli, err := ResolveCLI(cliPath, "", env, false)
	if err != nil {
		return "", "", err
	}
	return cli.Run(ctx, waitDelay, args...)
}

// Run executes a short-lived CLI command using the resolved launch inputs.
func (cli CLI) Run(
	ctx context.Context,
	waitDelay time.Duration,
	args ...string,
) (stdout, stderr string, err error) {
	var stdoutBuffer, stderrBuffer bytes.Buffer
	err = retryOnETXTBSY(ctx, func() error {
		stdoutBuffer.Reset()
		stderrBuffer.Reset()

		cmd := cli.newProbeCommand(ctx, args)
		configureCLICommandCancellation(cmd)
		cmd.WaitDelay = waitDelay
		cli.configure(cmd)
		cmd.Stdout = &stdoutBuffer
		cmd.Stderr = &stderrBuffer
		return cmd.Run()
	}, nil)
	return stdoutBuffer.String(), stderrBuffer.String(), err
}

func (cli CLI) newProbeCommand(ctx context.Context, args []string) *exec.Cmd {
	if cli.image == nil {
		return exec.CommandContext(ctx, cli.path, args...)
	}
	// The child inherits the opened executable as FD 3. This pins the probe
	// to the running image even if the original entry point is replaced.
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	cmd.Args[0] = cli.path
	cmd.ExtraFiles = []*os.File{cli.image}
	return cmd
}

func retryOnETXTBSY(ctx context.Context, run func() error, onRetry func(attempt int)) error {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := run()
		if !errors.Is(err, syscall.ETXTBSY) || attempt >= maxCLICommandAttempts {
			return err
		}
		if onRetry != nil {
			onRetry(attempt)
		}

		timer := time.NewTimer(time.Duration(attempt) * cliCommandRetryBaseInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
