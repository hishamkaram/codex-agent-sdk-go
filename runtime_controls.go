package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
