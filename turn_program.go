package codex

import (
	"context"
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
	return c.session.RequireSchema(ctx, feature.method, feature.name, feature.supported, ErrRuntimeControlsUnsupported, runtimeFeatureDiscoveryTimeout)
}
