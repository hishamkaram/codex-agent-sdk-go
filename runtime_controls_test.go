package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestParseRuntimeControls(t *testing.T) {
	t.Parallel()
	help := `
  -s, --sandbox <SANDBOX_MODE>
          Select sandbox
          [possible values: read-only, workspace-write, danger-full-access]
  -a, --ask-for-approval <APPROVAL_POLICY>
          Configure approvals
          Possible values:
          - untrusted: safe commands only
          - on-request: model decides
          - never: never ask
  -h, --help
`
	approvals, sandboxes := parseRuntimeControls(help)
	if want := []string{"untrusted", "on-request", "never"}; !reflect.DeepEqual(approvals, want) {
		t.Fatalf("approvals = %v, want %v", approvals, want)
	}
	if want := []string{"read-only", "workspace-write", "danger-full-access"}; !reflect.DeepEqual(sandboxes, want) {
		t.Fatalf("sandboxes = %v, want %v", sandboxes, want)
	}
}

func TestDiscoverRuntimeControlsHonorsCallerContext(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--help" ]; then
  cat <<'EOF'
-s, --sandbox <SANDBOX_MODE>
  [possible values: read-only, workspace-write, danger-full-access]
-a, --ask-for-approval <APPROVAL_POLICY>
  - untrusted: safe
  - on-request: ask
  - never: never
-h, --help
EOF
else
  sleep 0.3
  echo 'codex-cli 9.1.0'
fi
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := DiscoverRuntimeControls(ctx, types.NewCodexOptions().WithCLIPath(script), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want caller deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("DiscoverRuntimeControls() took %v after caller deadline", elapsed)
	}
}

func TestDiscoverRuntimeControlsHonorsEnvironmentUnset(t *testing.T) {
	const blockedKey = "CODEX_SDK_DISCOVERY_UNSET_TEST"
	t.Setenv(blockedKey, "parent")

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "${CODEX_SDK_DISCOVERY_UNSET_TEST+x}" = x ]; then
  echo 'unset variable is still present' >&2
  exit 9
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}

	controls, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithEnv(blockedKey+"="),
		nil,
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeControls() error = %v", err)
	}
	if controls.CLIVersion != "9.1.0" {
		t.Fatalf("CLI version = %q, want 9.1.0", controls.CLIVersion)
	}
}

func TestDiscoverRuntimeControlsRejectsEmptyManagedIntersection(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	requirements := &types.ConfigRequirements{
		AllowedApprovalPolicies: []types.ApprovalPolicyRequirement{
			types.NewApprovalPolicyRequirement("future-policy"),
		},
		AllowedSandboxModes: []string{"future-sandbox"},
	}
	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script),
		requirements,
	)
	if err == nil {
		t.Fatal("DiscoverRuntimeControls() error = nil, want empty-intersection failure")
	}
}

func TestDiscoverRuntimeControlsIncludesSchemaProvenGranularApproval(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ]; then
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--out" ]; then
      out="$2"
      break
    fi
    shift
  done
  mkdir -p "$out/v2"
  cat > "$out/v2/ThreadStartParams.json" <<'EOF'
{"definitions":{"AskForApproval":{"oneOf":[{"type":"string","enum":["untrusted","on-request","never"]},{"type":"object","properties":{"granular":{"type":"object"}}}]}}}
EOF
  exit 0
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	requirements := &types.ConfigRequirements{
		AllowedApprovalPolicies: []types.ApprovalPolicyRequirement{{
			ProviderValue: "granular",
			Granular:      granularSettings(types.DefaultGranularApprovalSettings()),
		}},
		AllowedSandboxModes: []string{"read-only"},
	}

	controls, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithExperimentalAPI(true),
		requirements,
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeControls() error = %v", err)
	}
	want := []types.RuntimeControlOption{{ProviderValue: "granular"}}
	if !reflect.DeepEqual(controls.ApprovalPolicies, want) {
		t.Fatalf("approval policies = %+v, want %+v", controls.ApprovalPolicies, want)
	}
}

func TestRuntimeControlsApplyManagedRequirements(t *testing.T) {
	t.Parallel()
	requirements := &types.ConfigRequirements{
		AllowedApprovalPolicies: []types.ApprovalPolicyRequirement{
			types.NewApprovalPolicyRequirement("on-request"),
		},
		AllowedSandboxModes: []string{"read-only"},
	}
	if got := filterApprovalRequirements([]string{"untrusted", "on-request", "never"}, requirements); !reflect.DeepEqual(got, []string{"on-request"}) {
		t.Fatalf("approval requirements = %v", got)
	}
	if got := filterStringRequirements([]string{"read-only", "workspace-write"}, requirements.AllowedSandboxModes); !reflect.DeepEqual(got, []string{"read-only"}) {
		t.Fatalf("sandbox requirements = %v", got)
	}
}

func TestApprovalPolicyRequirementDecodesGranularObject(t *testing.T) {
	t.Parallel()
	var got types.ConfigRequirementsReadResult
	err := json.Unmarshal([]byte(`{"requirements":{"allowedApprovalPolicies":["never",{"granular":{"rules":true,"mcp_elicitations":true,"sandbox_approval":false,"skill_approval":true}}]}}`), &got)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	want := []types.ApprovalPolicyRequirement{
		types.NewApprovalPolicyRequirement("never"),
		{
			ProviderValue: "granular",
			Granular: granularSettings(types.GranularApprovalSettings{
				Rules:           true,
				MCPElicitations: true,
				SkillApproval:   true,
			}),
		},
	}
	if !reflect.DeepEqual(got.Requirements.AllowedApprovalPolicies, want) {
		t.Fatalf("policies = %+v, want %+v", got.Requirements.AllowedApprovalPolicies, want)
	}
	roundTrip, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(roundTrip), `"skill_approval":true`) {
		t.Fatalf("Marshal() lost granular settings: %s", roundTrip)
	}
}

func TestRuntimeControlsRejectIncompatibleManagedGranularSettings(t *testing.T) {
	t.Parallel()
	requirements := &types.ConfigRequirements{
		AllowedApprovalPolicies: []types.ApprovalPolicyRequirement{{
			ProviderValue: string(types.ApprovalGranular),
			Granular: granularSettings(types.GranularApprovalSettings{
				Rules:           true,
				MCPElicitations: true,
			}),
		}},
	}
	got := filterApprovalRequirements([]string{string(types.ApprovalGranular)}, requirements)
	if len(got) != 0 {
		t.Fatalf("approval requirements = %v, want incompatible granular policy removed", got)
	}
}

func granularSettings(value types.GranularApprovalSettings) *types.GranularApprovalSettings {
	return &value
}
