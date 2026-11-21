package codex

import (
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/session"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func resolveDiscoveryCLI(opts *types.CodexOptions) (transport.CLI, error) {
	return session.ResolveCLIOptions(strings.TrimSpace(opts.CLIPath), "", opts.Env, opts.EnvMode)
}

func (c *Client) validateThreadApprovalPolicy(opts *types.ThreadOptions) error {
	policy := c.opts.DefaultApprovalPolicy
	if opts != nil && opts.ApprovalPolicy != "" {
		policy = opts.ApprovalPolicy
	}
	return c.validateApprovalPolicy(policy)
}

func (c *Client) validateApprovalPolicy(policy types.ApprovalPolicy) error {
	return c.session.ValidateApprovalPolicy(policy)
}
