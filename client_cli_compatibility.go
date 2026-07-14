package codex

import (
	"context"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

var (
	hookTrustBypassMinVersion   = transport.SemVer{Major: 0, Minor: 131, Patch: 0}
	canonicalHooksMinVersion    = transport.SemVer{Major: 0, Minor: 144, Patch: 0}
	approvalOnFailureEndVersion = transport.SemVer{Major: 0, Minor: 143, Patch: 0}
)

const legacyApprovalOnFailure types.ApprovalPolicy = "on-failure"

type cliCompatibilityState struct {
	version transport.SemVer
	known   bool
}

func probeCLICompatibilityVersion(
	ctx context.Context,
	opts *types.CodexOptions,
	env []string,
) transport.CLIVersionProbeResult {
	cliPath := opts.CLIPath
	if cliPath == "" {
		var err error
		cliPath, err = transport.FindCLI()
		if err != nil {
			return transport.CLIVersionProbeResult{Err: err}
		}
	}
	version, err := transport.ProbeCLIVersionContext(ctx, cliPath, env)
	return transport.CLIVersionProbeResult{Version: version, Err: err}
}

func cliCompatibilityArgs(
	opts *types.CodexOptions,
	version transport.SemVer,
	versionKnown bool,
) (globalArgs, extraArgs []string) {
	extraArgs = append([]string(nil), opts.ExtraArgs...)
	hooksEnabled := opts.HooksEnabled || opts.HookCallback != nil
	if hooksEnabled {
		feature := "codex_hooks"
		if versionKnown && version.AtLeast(canonicalHooksMinVersion) {
			feature = "hooks"
		}
		extraArgs = append(extraArgs, "--enable", feature)
	}
	if opts.HookCallback != nil && versionKnown && version.AtLeast(hookTrustBypassMinVersion) {
		globalArgs = []string{"--dangerously-bypass-hook-trust"}
	}
	return globalArgs, extraArgs
}

func validateApprovalPolicyCompatibility(
	policy types.ApprovalPolicy,
	version transport.SemVer,
	versionKnown bool,
) error {
	if policy != legacyApprovalOnFailure || !versionKnown || !version.AtLeast(approvalOnFailureEndVersion) {
		return nil
	}
	return types.NewUnsupportedApprovalPolicyError(policy, version.String())
}

func (c *Client) validateThreadApprovalPolicy(opts *types.ThreadOptions) error {
	policy := c.opts.DefaultApprovalPolicy
	if opts != nil && opts.ApprovalPolicy != "" {
		policy = opts.ApprovalPolicy
	}
	return c.validateApprovalPolicy(policy)
}

func (c *Client) validateApprovalPolicy(policy types.ApprovalPolicy) error {
	state := c.cliCompatibility.Load()
	if state == nil {
		return nil
	}
	return validateApprovalPolicyCompatibility(policy, state.version, state.known)
}
