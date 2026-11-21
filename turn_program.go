package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
	runtime, onClose := c.runtime, c.onDispatcherClose
	connected := c.connected.Load()
	c.lifecycleMu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("%s: %w", feature.method, types.ErrClientClosed)
	}
	if runtime == nil || !runtime.Enabled() {
		return types.NewFeatureNotEnabledError("experimentalApi", feature.method, "set WithExperimentalAPI(true) before connecting")
	}
	if !connected {
		return fmt.Errorf("%s: %w", feature.method, types.ErrClientNotConnected)
	}

	probeCtx, cancel := context.WithTimeout(ctx, runtimeFeatureDiscoveryTimeout)
	defer cancel()
	stop := onClose(cancel)
	defer stop()
	probeErr := runtime.RequireSchema(probeCtx, feature.method, feature.name, feature.supported, ErrRuntimeControlsUnsupported)
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
