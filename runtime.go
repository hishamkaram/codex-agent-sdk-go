package codex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/session"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadconfig"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const runtimeFeatureDiscoveryTimeout = 5 * time.Second

// DiscoverRuntimeFeatures proves optional app-server surfaces from generated
// schemas. Missing schemas disable only the corresponding feature; malformed
// schemas return an error so callers fail closed instead of advertising stale
// controls.
func DiscoverRuntimeFeatures(ctx context.Context, options *types.CodexOptions) (types.RuntimeFeatureCapabilities, error) {
	return discoverRuntimeFeatures(ctx, options, runtimeFeatureDiscoveryTimeout)
}

func discoverRuntimeFeatures(
	ctx context.Context,
	options *types.CodexOptions,
	timeout time.Duration,
) (types.RuntimeFeatureCapabilities, error) {
	if ctx == nil {
		return types.RuntimeFeatureCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeFeatures: context is required")
	}
	if options == nil {
		options = types.NewCodexOptions()
	}
	cli, err := resolveDiscoveryCLI(options)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeFeatures: resolve Codex runtime: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return runtimeprobe.DiscoverFeatures(probeCtx, cli, options.ExperimentalAPI, ErrRuntimeControlsUnsupported)
}

// ErrRuntimeControlsUnsupported is wrapped when the installed CLI cannot
// advertise the approval and sandbox controls required for safe SDK use.
var ErrRuntimeControlsUnsupported = errors.New("codex: runtime control discovery unsupported")

// DiscoverRuntimeControls reads the installed CLI's help and intersects the
// advertised values with optional provider-managed requirements.
func DiscoverRuntimeControls(
	ctx context.Context,
	options *types.CodexOptions,
	requirements *types.ConfigRequirements,
) (types.RuntimeControlCapabilities, error) {
	if ctx == nil {
		return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: ctx is required")
	}
	if options == nil {
		options = types.NewCodexOptions()
	}
	cli, err := resolveDiscoveryCLI(options)
	if err != nil {
		return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: resolve Codex runtime: %w", err)
	}
	return runtimeprobe.DiscoverControls(ctx, cli, options.ExperimentalAPI, requirements, ErrRuntimeControlsUnsupported)
}

func resolveDiscoveryCLI(opts *types.CodexOptions) (transport.CLI, error) {
	return session.ResolveCLIOptions(strings.TrimSpace(opts.CLIPath), "", opts.Env, opts.EnvMode)
}

func (c *Client) requireTurnProgram(ctx context.Context, program types.CyberAccessProgram) error {
	if err := c.checkApprovalReentry(ctx); err != nil {
		return fmt.Errorf("turn/start: %w", err)
	}
	if err := threadconfig.ValidateTurnProgram(program); err != nil {
		return err
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
