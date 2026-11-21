package runtimeprobe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestGenerateSchemaRemovesPartialOutputOnFailure(t *testing.T) {
	t.Parallel()
	cli, marker := runtimeProbeFixture(t, `#!/bin/sh
case "$*" in
  'app-server generate-json-schema --help')
    printf '%s\n' '  --experimental' '  --out <DIR>'
    ;;
  'app-server generate-json-schema --experimental --out '*)
    printf '%s' "$5" > "$SDK_TEST_SCHEMA_MARKER"
    printf '%s' 'partial output' > "$5/partial.json"
    exit 42
    ;;
  *) exit 99 ;;
esac
`)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	unsupported := errors.New("fixture lacks schema support")
	outDir, cleanup, err := GenerateSchema(ctx, cli, "schema test", true, unsupported)
	t.Cleanup(cleanup)
	var exitErr *exec.ExitError
	if outDir != "" || !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 || errors.Is(err, unsupported) {
		t.Fatalf("GenerateSchema = %q, %v, want no output and underlying exit code 42", outDir, err)
	}
	assertSchemaDirectoryRemoved(t, marker)
}

func TestDiscoverFeaturesUsesStableSchemaWhenExperimentalOptionIsMissing(t *testing.T) {
	t.Parallel()
	cli, marker := runtimeProbeFixture(t, `#!/bin/sh
case "$*" in
  '--version') printf '%s\n' 'codex-cli 0.171.0' ;;
  'app-server generate-json-schema --help') printf '%s\n' '  --out <DIR>' ;;
  'app-server generate-json-schema --out '*)
    printf '%s' "$4" > "$SDK_TEST_SCHEMA_MARKER"
    mkdir "$4/v2" || exit 90
    printf '%s' '{"type":"object","properties":{"threadId":{"type":"string"},"turnId":{"type":"string"}},"required":["turnId","threadId"]}' > "$4/v2/TurnInterruptParams.json"
    printf '%s' '{"type":"object"}' > "$4/v2/TurnInterruptResponse.json"
    ;;
  *) exit 99 ;;
esac
`)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	got, err := DiscoverFeatures(ctx, cli, true, errors.New("fixture lacks experimental schema support"))
	want := types.RuntimeFeatureCapabilities{CLIVersion: "0.171.0", TurnInterrupt: true}
	if err != nil || got != want {
		t.Fatalf("DiscoverFeatures = %+v, %v, want %+v", got, err, want)
	}
	assertSchemaDirectoryRemoved(t, marker)
}

func TestDiscoverControlsAppliesRequirementsToNegotiatedSchema(t *testing.T) {
	t.Parallel()
	cli, marker := runtimeProbeFixture(t, `#!/bin/sh
case "$*" in
  '--version') printf '%s\n' 'codex-cli 0.171.0' ;;
  '--help')
    printf '%s\n' '--ask-for-approval <POLICY>' '  - never: do not ask' '--sandbox <MODE> [possible values: read-only, workspace-write]' >&2
    ;;
  'app-server generate-json-schema --help')
    printf '%s\n' '  --experimental' '  --out <DIR>'
    ;;
  'app-server generate-json-schema --experimental --out '*)
    printf '%s' "$5" > "$SDK_TEST_SCHEMA_MARKER"
    mkdir "$5/v2" || exit 90
    printf '%s' '{"definitions":{"AskForApproval":{"oneOf":[{"enum":["never"]},{"properties":{"granular":{"type":"object"}}}]}}}' > "$5/v2/ThreadStartParams.json"
    ;;
  *) exit 99 ;;
esac
`)
	granular := types.DefaultGranularApprovalSettings()
	requirements := &types.ConfigRequirements{
		AllowedApprovalPolicies: []types.ApprovalPolicyRequirement{{ProviderValue: "granular", Granular: &granular}},
		AllowedSandboxModes:     []string{"workspace-write"},
	}
	got, err := DiscoverControls(t.Context(), cli, true, requirements, errors.New("fixture lacks experimental schema support"))
	want := types.RuntimeControlCapabilities{
		ApprovalPolicies: []types.RuntimeControlOption{{ProviderValue: "granular"}},
		SandboxModes:     []types.RuntimeControlOption{{ProviderValue: "workspace-write"}},
		CLIVersion:       "0.171.0",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverControls = %+v, %v, want %+v", got, err, want)
	}
	assertSchemaDirectoryRemoved(t, marker)
}

func runtimeProbeFixture(t *testing.T, script string) (transport.CLI, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runtime probe fixture uses a POSIX shell executable")
	}
	fixtureDir := t.TempDir()
	marker := filepath.Join(fixtureDir, "schema-path")
	path := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(path+".tmp", []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
	cli, err := transport.ResolveCLI(path, "", []string{"SDK_TEST_SCHEMA_MARKER=" + marker}, false)
	if err != nil {
		t.Fatal(err)
	}
	return cli, marker
}

func assertSchemaDirectoryRemoved(t *testing.T, marker string) {
	t.Helper()
	path, err := os.ReadFile(marker)
	if err != nil || len(path) == 0 {
		t.Fatalf("fixture did not record a schema directory: %v", err)
	}
	if _, err := os.Stat(string(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("schema directory still accessible after probe: %v", err)
	}
}
