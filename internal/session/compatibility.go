package session

import (
	"fmt"

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

// ResolveCLIOptions resolves an isolated or inherited child environment.
func ResolveCLIOptions(path, cwd string, env []string, mode types.EnvMode) (transport.CLI, error) {
	switch mode {
	case types.EnvOverlay, types.EnvReplacement:
		return transport.ResolveCLI(path, cwd, env, mode == types.EnvReplacement)
	default:
		return transport.CLI{}, fmt.Errorf("invalid Codex environment mode")
	}
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

// ValidateApprovalPolicy checks the server version observed on this session.
func (s *Session) ValidateApprovalPolicy(policy types.ApprovalPolicy) error {
	state := s.cliCompatibility.Load()
	if state == nil {
		return nil
	}
	return validateApprovalPolicyCompatibility(policy, state.version, state.known)
}
