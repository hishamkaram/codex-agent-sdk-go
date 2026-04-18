package codex

import "context"

// StartupOption configures the warm-pool pre-spawn step.
type StartupOption func(*startupConfig)

type startupConfig struct {
	cliPath string
}

// WithStartupCLIPath overrides the codex binary location for the warm
// pool.
func WithStartupCLIPath(path string) StartupOption {
	return func(c *startupConfig) { c.cliPath = path }
}

// Startup pre-warms the SDK's internal subprocess pool so the first Query
// or NewClient+Connect avoids a ~200-400 ms cold-start cost.
//
// In v0.1.0 this is a NO-OP stub — the SDK always spawns on demand.
// The API is stable so users who adopt Startup today won't need to change
// call sites when the pool lands in v0.2.
func Startup(ctx context.Context, opts ...StartupOption) error {
	_ = ctx
	cfg := &startupConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	return nil
}
