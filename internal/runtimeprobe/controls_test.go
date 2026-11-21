package runtimeprobe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
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

func TestRequireExperimentalSchemaCommandAllowsShortLivedInheritedOutputPipe(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  sleep 0.25 &
  printf '%s\n' 'Usage: codex app-server generate-json-schema' '  --experimental' '  --out <DIR>'
  exit 0
fi
exit 99
`
	if err := os.WriteFile(script+".tmp", []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(script+".tmp", script); err != nil {
		t.Fatal(err)
	}
	cli, err := transport.ResolveCLI(script, "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireSchemaCommand(context.Background(), cli, "codex.DiscoverRuntimeControls", true, errors.New("unsupported")); err != nil {
		t.Fatalf("requireSchemaCommand() error = %v", err)
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

func TestRuntimeControlsRejectIncompatibleManagedGranularSettings(t *testing.T) {
	t.Parallel()
	requirements := &types.ConfigRequirements{
		AllowedApprovalPolicies: []types.ApprovalPolicyRequirement{{
			ProviderValue: string(types.ApprovalGranular),
			Granular: &types.GranularApprovalSettings{
				Rules:           true,
				MCPElicitations: true,
			},
		}},
	}
	got := filterApprovalRequirements([]string{string(types.ApprovalGranular)}, requirements)
	if len(got) != 0 {
		t.Fatalf("approval requirements = %v, want incompatible granular policy removed", got)
	}
}
