package transport

import (
	"bytes"
	"context"
	"errors"
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
	var stdoutBuffer, stderrBuffer bytes.Buffer
	err = retryOnETXTBSY(ctx, func() error {
		stdoutBuffer.Reset()
		stderrBuffer.Reset()

		cmd := exec.CommandContext(ctx, cliPath, args...)
		cmd.WaitDelay = waitDelay
		cmd.Env = BuildRuntimeEnvironment(env)
		cmd.Stdout = &stdoutBuffer
		cmd.Stderr = &stderrBuffer
		return cmd.Run()
	}, nil)
	return stdoutBuffer.String(), stderrBuffer.String(), err
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
