package runtimeprobe

import (
	"context"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
)

// Connected binds schema discovery to the launch and opt-in of one app-server.
// Only the holder of probeSlot can read or change the proof cache.
type Connected struct {
	cli          transport.CLI
	app          *transport.AppServer
	experimental bool
	probeSlot    chan struct{}
	proved       map[string]struct{}
}

// NewConnected creates a verifier for one app-server launch.
func NewConnected(cli transport.CLI, app *transport.AppServer, experimental bool) *Connected {
	return &Connected{
		cli: cli, app: app, experimental: experimental,
		probeSlot: make(chan struct{}, 1),
		proved:    make(map[string]struct{}),
	}
}

// Enabled reports the experimental opt-in captured for this connection.
func (r *Connected) Enabled() bool { return r.experimental }

// RequireSchema proves the feature against the executable bound to this connection.
// The caller owns the context deadline and the unsupported error identity.
func (r *Connected) RequireSchema(ctx context.Context, method, name string, supported func(string) (bool, error), unsupported error) error {
	select {
	case r.probeSlot <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("%s: check experimental schema: %w", method, ctx.Err())
	}
	defer func() { <-r.probeSlot }()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: check experimental schema: %w", method, err)
	}
	if _, proved := r.proved[name]; proved {
		if r.cli.MatchesRunningExecutable(r.app.Pid()) {
			return nil
		}
		clear(r.proved)
	}
	cli, release, err := r.cli.PinRunningExecutable(r.app.Pid())
	if err != nil {
		return fmt.Errorf("%s: verify Codex runtime executable: %w", method, errors.Join(unsupported, err))
	}
	defer release()
	outDir, cleanup, err := GenerateSchema(ctx, cli, method, true, unsupported)
	if err != nil {
		return fmt.Errorf("%s: check %s support: %w", method, name, err)
	}
	defer cleanup()
	matches, err := supported(outDir)
	if err != nil {
		return fmt.Errorf("%s: read experimental schema: %w", method, err)
	}
	if !matches || !r.cli.MatchesRunningExecutable(r.app.Pid()) {
		return fmt.Errorf("%s: connected runtime did not prove %s support: %w", method, name, unsupported)
	}
	r.proved[name] = struct{}{}
	return nil
}
