package codex

import (
	"context"
	"fmt"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
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
