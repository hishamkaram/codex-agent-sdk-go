package codex

import (
	"cmp"
	"context"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadconfig"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// validateThreadWebSearchMode checks managed constraints before Codex can
// substitute its own allowed mode for the SDK's requested mode.
func (c *Client) validateThreadWebSearchMode(ctx context.Context, mode types.WebSearchMode) error {
	if mode == "" {
		return nil
	}
	requirements, err := c.ReadConfigRequirements(ctx)
	if err != nil {
		return err
	}
	return threadconfig.ValidateWebSearchMode(mode, requirements)
}

type threadResumeRequest struct {
	params          map[string]any
	cwd             string
	reasoningEffort string
}

func (c *Client) prepareThreadResume(ctx context.Context, threadID string, opts *types.ResumeOptions) (threadResumeRequest, error) {
	if opts == nil {
		opts = &types.ResumeOptions{}
	}
	overrides := threadconfig.ResolveResumeOverrides(c.opts, opts)
	if err := c.validateThreadWebSearchMode(ctx, overrides.WebSearchMode); err != nil {
		return threadResumeRequest{}, err
	}
	params := overrides.Params(threadID, opts.Cwd)
	if overrides.ReasoningEffort != "" {
		model, err := c.readResumeModel(ctx, threadID)
		if err != nil {
			return threadResumeRequest{}, err
		}
		params["model"] = model
	}
	return threadResumeRequest{params: params, cwd: cmp.Or(opts.Cwd, c.opts.DefaultCwd), reasoningEffort: overrides.ReasoningEffort}, nil
}

// An effort override disables Codex's normal model restoration. Ask the
// provider for the loaded or persisted model before building the resume.
func (c *Client) readResumeModel(ctx context.Context, threadID string) (string, error) {
	result, err := c.sendRaw(ctx, "ReadThread", "thread/read", map[string]any{
		"threadId": threadID, "includeTurns": false,
	})
	if err != nil {
		return "", err
	}
	return threadconfig.DecodeResumeModel(result, threadID)
}

// appServerWorkingDirectory reads the launch directory from the transport
// retained by this connection. Close may clear that transport concurrently.
func (c *Client) appServerWorkingDirectory() string {
	c.lifecycleMu.Lock()
	tr := c.tr
	c.lifecycleMu.Unlock()
	if tr == nil {
		return ""
	}
	return tr.WorkingDirectory()
}
