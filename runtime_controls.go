package codex

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const runtimeControlCommandWaitDelay = 500 * time.Millisecond

var approvalChoiceRE = regexp.MustCompile(`^\s*-\s+([a-z][a-z0-9-]*):`)

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
	cliPath := strings.TrimSpace(options.CLIPath)
	if cliPath == "" {
		var err error
		cliPath, err = transport.FindCLI()
		if err != nil {
			return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: find CLI: %w", err)
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, stderr, err := transport.RunCLICommand(
		probeCtx,
		cliPath,
		options.Env,
		runtimeControlCommandWaitDelay,
		"--help",
	)
	if err != nil {
		if ctxErr := probeCtx.Err(); ctxErr != nil {
			return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: CLI help: %w", ctxErr)
		}
		return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: CLI help: %w", err)
	}
	help := stdout + "\n" + stderr
	approvals, sandboxes := parseRuntimeControls(help)
	if len(approvals) == 0 || len(sandboxes) == 0 {
		return types.RuntimeControlCapabilities{}, fmt.Errorf(
			"codex.DiscoverRuntimeControls: installed CLI did not advertise runtime controls: %w",
			ErrRuntimeControlsUnsupported,
		)
	}
	if options.ExperimentalAPI {
		experimentalApprovals, discoveryErr := discoverExperimentalApprovalPolicies(
			probeCtx,
			cliPath,
			options.Env,
		)
		if discoveryErr != nil {
			return types.RuntimeControlCapabilities{}, discoveryErr
		}
		for _, value := range experimentalApprovals {
			approvals = appendUnique(approvals, value)
		}
	}
	version, err := transport.ProbeCLIVersionContext(ctx, cliPath, options.Env)
	if err != nil {
		return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: CLI version: %w", err)
	}
	approvals = filterApprovalRequirements(approvals, requirements)
	sandboxes = filterStringRequirements(sandboxes, sandboxRequirements(requirements))
	if len(approvals) == 0 || len(sandboxes) == 0 {
		return types.RuntimeControlCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeControls: managed requirements removed every runtime control")
	}
	return types.RuntimeControlCapabilities{
		ApprovalPolicies: runtimeControlOptions(approvals),
		SandboxModes:     runtimeControlOptions(sandboxes),
		CLIVersion:       version.String(),
	}, nil
}

func parseRuntimeControls(help string) (approvals, sandboxes []string) {
	lines := strings.Split(help, "\n")
	inApproval := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, "--ask-for-approval") {
			inApproval = true
			continue
		}
		if strings.Contains(line, "--sandbox <") {
			sandboxes = parseBracketValues(line)
			inApproval = false
			continue
		}
		if inApproval {
			if match := approvalChoiceRE.FindStringSubmatch(line); len(match) == 2 {
				approvals = appendUnique(approvals, match[1])
				continue
			}
			if strings.HasPrefix(trimmed, "-") && strings.Contains(trimmed, "--") {
				inApproval = false
			}
		}
		if len(sandboxes) == 0 && strings.Contains(line, "[possible values:") {
			sandboxes = parseBracketValues(line)
		}
	}
	return approvals, sandboxes
}

func parseBracketValues(line string) []string {
	const marker = "[possible values:"
	start := strings.Index(line, marker)
	if start < 0 {
		return nil
	}
	values := strings.TrimSpace(line[start+len(marker):])
	values = strings.TrimSuffix(values, "]")
	var out []string
	for _, value := range strings.Split(values, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			out = appendUnique(out, value)
		}
	}
	return out
}

func filterApprovalRequirements(values []string, requirements *types.ConfigRequirements) []string {
	if requirements == nil || requirements.AllowedApprovalPolicies == nil {
		return values
	}
	allowed := make([]string, 0, len(requirements.AllowedApprovalPolicies))
	for _, requirement := range requirements.AllowedApprovalPolicies {
		if requirement.ProviderValue == string(types.ApprovalGranular) &&
			(requirement.Granular == nil ||
				*requirement.Granular != types.DefaultGranularApprovalSettings()) {
			continue
		}
		allowed = append(allowed, requirement.ProviderValue)
	}
	return filterStringRequirements(values, allowed)
}

func sandboxRequirements(requirements *types.ConfigRequirements) []string {
	if requirements == nil {
		return nil
	}
	return requirements.AllowedSandboxModes
}

func filterStringRequirements(values, allowed []string) []string {
	if allowed == nil {
		return values
	}
	set := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := set[value]; ok {
			out = append(out, value)
		}
	}
	return out
}

func runtimeControlOptions(values []string) []types.RuntimeControlOption {
	out := make([]types.RuntimeControlOption, 0, len(values))
	for _, value := range values {
		out = append(out, types.RuntimeControlOption{ProviderValue: value})
	}
	return out
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
