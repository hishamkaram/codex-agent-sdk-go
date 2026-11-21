package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// connectedRuntime pins the launch inputs and actual handshake opt-in. It
// belongs to one Client connection. The probe slot protects the schema proof.
type connectedRuntime struct {
	cli          transport.CLI
	app          *transport.AppServer
	experimental bool
	onClose      func(func()) func() bool // bound before the connection is published
	probeSlot    chan struct{}
	proved       map[string]struct{}
}

func newConnectedRuntime(cli transport.CLI, app *transport.AppServer, experimental bool) *connectedRuntime {
	return &connectedRuntime{cli: cli, app: app, experimental: experimental, probeSlot: make(chan struct{}, 1), proved: make(map[string]struct{})}
}

func (c *Client) requireTurnProgram(ctx context.Context, program types.CyberAccessProgram) error {
	if err := c.checkApprovalReentry(ctx); err != nil {
		return fmt.Errorf("turn/start: %w", err)
	}
	switch program {
	case types.CyberAccessProgramStandard, types.CyberAccessProgramDaybreakBlue, types.CyberAccessProgramDaybreakRed:
	default:
		return fmt.Errorf("turn/start: invalid cyberAccessProgram")
	}

	return c.requireConnectedSchema(ctx, connectedSchemaFeature{
		method: "turn/start", name: "cyberAccessProgram",
		supported: runtimeprobe.SupportsTurnProgram,
	})
}

type connectedSchemaFeature struct {
	method    string
	name      string
	supported func(root string) (bool, error)
}

func (c *Client) requireConnectedSchema(ctx context.Context, feature connectedSchemaFeature) error {
	c.lifecycleMu.Lock()
	runtime := c.runtime
	c.lifecycleMu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("%s: %w", feature.method, types.ErrClientClosed)
	}
	if runtime == nil || !runtime.experimental {
		return types.NewFeatureNotEnabledError("experimentalApi", feature.method, "set WithExperimentalAPI(true) before connecting")
	}
	if !c.connected.Load() {
		return fmt.Errorf("%s: %w", feature.method, types.ErrClientNotConnected)
	}

	probeCtx, cancel := context.WithTimeout(ctx, runtimeFeatureDiscoveryTimeout)
	defer cancel()
	stop := runtime.onClose(cancel)
	defer stop()
	probeErr := runtime.requireSchema(probeCtx, feature)
	if callerErr := ctx.Err(); callerErr != nil {
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, callerErr)
	}
	if c.closed.Load() {
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, types.ErrClientClosed)
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s: experimental schema probe exceeded the SDK deadline: %w", feature.method, ErrRuntimeControlsUnsupported)
	}
	return probeErr
}

func (r *connectedRuntime) requireSchema(ctx context.Context, feature connectedSchemaFeature) error {
	select {
	case r.probeSlot <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, ctx.Err())
	}
	defer func() { <-r.probeSlot }()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, err)
	}
	if _, proved := r.proved[feature.name]; proved {
		if r.cli.MatchesRunningExecutable(r.app.Pid()) {
			return nil
		}
		clear(r.proved)
	}
	cli, release, err := r.cli.PinRunningExecutable(r.app.Pid())
	if err != nil {
		return fmt.Errorf("%s: verify Codex runtime executable: %w", feature.method, errors.Join(ErrRuntimeControlsUnsupported, err))
	}
	defer release()
	outDir, cleanup, err := runtimeprobe.GenerateSchema(ctx, cli, feature.method, true, ErrRuntimeControlsUnsupported)
	if err != nil {
		return fmt.Errorf("%s: check %s support: %w", feature.method, feature.name, err)
	}
	defer cleanup()
	supported, err := feature.supported(outDir)
	if err != nil {
		return fmt.Errorf("%s: read experimental schema: %w", feature.method, err)
	}
	if !supported || !r.cli.MatchesRunningExecutable(r.app.Pid()) {
		return fmt.Errorf("%s: connected runtime did not prove %s support: %w", feature.method, feature.name, ErrRuntimeControlsUnsupported)
	}
	r.proved[feature.name] = struct{}{}
	return nil
}
