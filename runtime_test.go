package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
  sleep 1
  echo 'codex-cli 9.1.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := DiscoverRuntimeControls(ctx, types.NewCodexOptions().WithCLIPath(script), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want caller deadline", err)
	}
	// WaitDelay bounds cancellation cleanup, but an overloaded CI worker can
	// delay the test goroutine after the child has been stopped. Keep a generous
	// scheduling allowance while staying below the fixture's uncanceled delay.
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("DiscoverRuntimeControls() took %v after caller deadline", elapsed)
	}
}

func TestDiscoverRuntimeControlsAllowsShortLivedInheritedOutputPipe(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--help" ]; then
  sleep 0.25 &
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	controls, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script),
		nil,
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeControls() error = %v", err)
	}
	if controls.CLIVersion != "9.1.0" {
		t.Fatalf("CLI version = %q, want 9.1.0", controls.CLIVersion)
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
	writeFakeRuntimeControlsCLI(t, script, contents)

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
	writeFakeRuntimeControlsCLI(t, script, contents)
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

func TestDiscoverRuntimeControlsClassifiesUnsupportedCLI(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--help" ]; then
  printf '%s\n' 'legacy codex help without runtime controls'
else
  echo 'codex-cli 0.1.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script),
		nil,
	)
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want ErrRuntimeControlsUnsupported", err)
	}
}

func TestDiscoverRuntimeControlsClassifiesMissingExperimentalSchema(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  printf '%s\n' 'Usage: codex app-server generate-json-schema' '  --experimental' '  --out <DIR>'
  exit 0
fi
if [ "$1" = "app-server" ]; then
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--out" ]; then
      out="$2"
      break
    fi
    shift
  done
  mkdir -p "$out/v2"
  printf '%s\n' '{"definitions":{}}' > "$out/v2/ThreadStartParams.json"
  exit 0
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithExperimentalAPI(true),
		nil,
	)
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want ErrRuntimeControlsUnsupported", err)
	}
}

func TestDiscoverRuntimeControlsClassifiesMissingExperimentalSchemaOption(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  printf '%s\n' 'Usage: codex app-server generate-json-schema' '  --out <DIR>'
  exit 0
fi
if [ "$1" = "app-server" ]; then
  exit 99
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithExperimentalAPI(true),
		nil,
	)
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want ErrRuntimeControlsUnsupported", err)
	}
}

func TestDiscoverRuntimeControlsClassifiesMissingExperimentalSchemaCommand(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  echo "error: unexpected argument 'generate-json-schema' found" >&2
  exit 2
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 0.50.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithExperimentalAPI(true),
		nil,
	)
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want ErrRuntimeControlsUnsupported", err)
	}
}

func TestDiscoverRuntimeControlsClassifiesUnrecognizedExperimentalSchemaCommand(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  echo "error: unrecognized subcommand 'generate-json-schema'" >&2
  exit 2
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 0.40.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithExperimentalAPI(true),
		nil,
	)
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want ErrRuntimeControlsUnsupported", err)
	}
}

func TestDiscoverRuntimeControlsPreservesTransientSchemaFailure(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  printf '%s\n' 'Usage: codex app-server generate-json-schema' '  --experimental' '  --out <DIR>'
  exit 0
fi
if [ "$1" = "app-server" ]; then
  echo 'temporary schema service failure' >&2
  exit 75
fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-s, --sandbox <SANDBOX_MODE>' '  [possible values: read-only]' '-a, --ask-for-approval <APPROVAL_POLICY>' '  - on-request: ask' '-h, --help'
else
  echo 'codex-cli 9.1.0'
fi
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script).WithExperimentalAPI(true),
		nil,
	)
	if err == nil || errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("DiscoverRuntimeControls() error = %v, want retryable non-unsupported failure", err)
	}
}

func TestDiscoverRuntimeControlsIncludesSchemaProvenGranularApproval(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  printf '%s\n' 'Usage: codex app-server generate-json-schema' '  --experimental' '  --out <DIR>'
  exit 0
fi
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
	writeFakeRuntimeControlsCLI(t, script, contents)
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

func granularSettings(value types.GranularApprovalSettings) *types.GranularApprovalSettings {
	return &value
}

func writeFakeRuntimeControlsCLI(t *testing.T, path, contents string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	// Publish atomically so parallel probes never execute a writable fixture.
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("publish fake CLI: %v", err)
	}
}

func TestDiscoverRuntimeFeaturesReportsStableFeaturesWithoutExperimentalAPI(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo 'custom-codex-wrapper'
  exit 0
fi
if [ "$1" = "app-server" ] && [ "$2" = "generate-json-schema" ] && [ "$3" = "--help" ]; then
  printf '%s\n' 'Usage: codex app-server generate-json-schema' '  --experimental' '  --out <DIR>'
  exit 0
fi
if [ "$1" = "app-server" ]; then
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--experimental" ]; then
      exit 98
    fi
    if [ "$1" = "--out" ]; then
      out="$2"
      break
    fi
    shift
  done
	  mkdir -p "$out/v2"
	  printf '%s\n' '{"type":"object","required":["threadId","turnId"],"properties":{"threadId":{"type":"string"},"turnId":{"type":"string"}}}' > "$out/v2/TurnInterruptParams.json"
	  printf '%s\n' '{"type":"object"}' > "$out/v2/TurnInterruptResponse.json"
	  printf '%s\n' '{"type":"object","definitions":{"SubAgentActivityKind":{"type":"string","enum":["started","interacted","interrupted","completed"]},"ThreadItem":{"oneOf":[{"title":"SubAgentActivityThreadItem","type":"object","required":["agentPath","agentThreadId","id","kind","type"],"properties":{"agentPath":{"type":"string"},"agentThreadId":{"type":"string"},"id":{"type":"string"},"kind":{"$ref":"#/definitions/SubAgentActivityKind"},"type":{"type":"string","enum":["subAgentActivity"]}}}]}}}' > "$out/v2/ItemStartedNotification.json"
  exit 0
fi
exit 99
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	features, err := DiscoverRuntimeFeatures(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script),
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeFeatures() error = %v", err)
	}
	if !features.SubAgentActivity || !features.TurnInterrupt {
		t.Fatalf("stable features = %+v", features)
	}
	if features.BackgroundTerminalInventory || features.BackgroundTerminalTerminate || features.BackgroundTerminalsClean {
		t.Fatalf("experimental terminal features leaked into default options: %+v", features)
	}
	if features.CLIVersion != "" {
		t.Fatalf("informational CLI version = %q", features.CLIVersion)
	}
}

func TestRuntimeFeatureDiscoveryBoundsProviderProbe(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--version" ]; then
  exec sleep 30
fi
exit 99
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	started := time.Now()
	_, err := discoverRuntimeFeatures(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(script),
		25*time.Millisecond,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("discoverRuntimeFeatures() error = %v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("discoverRuntimeFeatures() took %v after probe deadline", elapsed)
	}
}

func TestDiscoverRuntimeFeaturesPreservesCallerInSchemaProbeError(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "codex")
	contents := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo 'codex-cli 9.1.0'
  exit 0
fi
if [ "$1" = "app-server" ]; then
  echo 'temporary schema failure' >&2
  exit 75
fi
exit 99
`
	writeFakeRuntimeControlsCLI(t, script, contents)

	_, err := DiscoverRuntimeFeatures(context.Background(), types.NewCodexOptions().WithCLIPath(script))
	if err == nil || !strings.Contains(err.Error(), "codex.DiscoverRuntimeFeatures: app-server help") {
		t.Fatalf("DiscoverRuntimeFeatures() error = %v", err)
	}
	if strings.Contains(err.Error(), "codex.DiscoverRuntimeControls") {
		t.Fatalf("DiscoverRuntimeFeatures() misattributed error = %v", err)
	}
}

// Reduced from the generated Codex 0.171.0 experimental TurnStartParams schema.
const turnProgramSchema = `{
	"type": "object",
	"required": ["input", "threadId"],
	"properties": {
		"cyberAccessProgram": {
			"anyOf": [{"$ref":"#/definitions/CyberAccessProgram"}, {"type":"null"}]
		}
	},
	"definitions": {
		"CyberAccessProgram": {
			"type": "string",
			"enum": ["standard", "daybreakBlue", "daybreakRed"]
		}
	}
}`

func TestTurnProgramSchemaRequiresExactOptionalEnum(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, schema string
		want         bool
	}{
		{"pinned contract", turnProgramSchema, true},
		{"field absent", strings.Replace(turnProgramSchema, "cyberAccessProgram", "unrelated", 1), false},
		{"required instead of optional", strings.Replace(turnProgramSchema, `"input", "threadId"`, `"input", "threadId", "cyberAccessProgram"`, 1), false},
		{"enum detached from request", strings.Replace(turnProgramSchema, "#/definitions/CyberAccessProgram", "#/definitions/Other", 1), false},
		{"unsupported enum", strings.Replace(turnProgramSchema, `, "daybreakRed"`, "", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			v2 := filepath.Join(root, "v2")
			if err := os.Mkdir(v2, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(v2, "TurnStartParams.json"), []byte(tc.schema), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := runtimeprobe.SupportsTurnProgram(root); err != nil || got != tc.want {
				t.Fatalf("schema match = %t, err = %v, want %t", got, err, tc.want)
			}
		})
	}
}

func TestThreadTurnProgramRequiresHandshakeAndProviderSchema(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	for _, tc := range []struct {
		name         string
		experimental bool
		schema       string
	}{
		{"handshake opt-in required", false, turnProgramSchema},
		{"field must be proved", true, `{"type":"object","properties":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := writeTurnProgramCLI(t, tc.schema)
			opts := types.NewCodexOptions().WithCLIPath(fixture.executable).WithEnvReplacement("GORACE=atexit_sleep_ms=0").WithExperimentalAPI(tc.experimental)
			_, thread := connectTurnProgramFixture(t, opts)
			// Mutating the original options after Connect cannot change what
			// the peer actually received in initialize.
			opts.WithExperimentalAPI(true)
			program := types.CyberAccessProgramStandard
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
			if !tc.experimental {
				if !types.IsFeatureNotEnabledError(err) {
					t.Fatalf("missing opt-in error = %v", err)
				}
				if count := readProgramProbeCount(t, fixture.probes); count != 0 {
					t.Fatalf("missing opt-in started %d schema probes", count)
				}
			} else if !errors.Is(err, ErrRuntimeControlsUnsupported) {
				t.Fatalf("missing runtime support error = %v", err)
			}
			if sent := readTurnStartPrograms(t, fixture.requests); len(sent) != 0 {
				t.Fatalf("unsupported option sent turn/start: %v", sent)
			}
		})
	}
}

func TestThreadTurnProgramUsesProofOnceAndPreservesProviderRejection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().WithCLIPath(fixture.executable).WithEnvReplacement("GORACE=atexit_sleep_ms=0").WithExperimentalAPI(true)
	_, thread := connectTurnProgramFixture(t, opts)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if probes := readProgramProbeCount(t, fixture.probes); probes != 0 {
		t.Fatalf("ordinary connection generated %d schemas", probes)
	}
	for _, program := range []types.CyberAccessProgram{
		types.CyberAccessProgramStandard, types.CyberAccessProgramDaybreakBlue, types.CyberAccessProgramDaybreakRed,
	} {
		turn, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
		if program == types.CyberAccessProgramDaybreakRed {
			var providerErr *types.RPCError
			if !errors.As(err, &providerErr) || providerErr.Code != -32123 || providerErr.Message != "test provider denied requested program" {
				t.Fatalf("account rejection was not propagated: %v", err)
			}
		} else if err != nil || turn.Status != "completed" {
			t.Fatalf("turn failed: result=%+v err=%v", turn, err)
		}
	}
	if _, err := thread.Run(ctx, "omitted option", nil); err != nil {
		t.Fatal(err)
	}
	got := readTurnStartPrograms(t, fixture.requests)
	if want := []string{`"standard"`, `"daybreakBlue"`, `"daybreakRed"`, ""}; !slices.Equal(got, want) {
		t.Fatalf("wire program values = %q, want %q", got, want)
	}
	if probes := readProgramProbeCount(t, fixture.probes); probes != 1 {
		t.Fatalf("schema generated %d times, want one connection-local proof", probes)
	}
	_, secondConnection := connectTurnProgramFixture(t, opts)
	program := types.CyberAccessProgramStandard
	if _, err := secondConnection.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program}); err != nil {
		t.Fatal(err)
	}
	if probes := readProgramProbeCount(t, fixture.probes); probes != 2 {
		t.Fatalf("two connections generated %d schemas, want one each", probes)
	}
}

func TestTurnProgramRejectsApprovalCallbackReentry(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var client *Client
	other := &Client{}
	sameClient := make(chan error, 1)
	otherClient := make(chan error, 1)
	opts := types.NewCodexOptions().WithApprovalCallback(func(callbackCtx context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		sameClient <- client.requireTurnProgram(callbackCtx, types.CyberAccessProgramStandard)
		otherClient <- other.requireTurnProgram(callbackCtx, types.CyberAccessProgramStandard)
		return types.ApprovalDeny{}
	})
	var server *mockCodexServer
	client, server = setupMockClient(t, opts, nil)
	server.push(json.RawMessage(`{"id":"guard","method":"item/permissions/requestApproval","params":{"threadId":"T","turnId":"U","itemId":"i","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}}`))
	if err := awaitExecutionTest(t, ctx, sameClient); !errors.Is(err, ErrApprovalCallbackReentry) {
		t.Fatalf("turn program reentry = %v, want ErrApprovalCallbackReentry", err)
	}
	if err := awaitExecutionTest(t, ctx, otherClient); errors.Is(err, ErrApprovalCallbackReentry) {
		t.Fatalf("another client rejected valid callback context: %v", err)
	}
}

type turnProgramFixture struct {
	executable string
	probes     string
	requests   string
	launchEnv  string
}

func writeTurnProgramCLI(t *testing.T, schema string) turnProgramFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := turnProgramFixture{
		executable: filepath.Join(dir, turnProgramPeerName),
		probes:     filepath.Join(dir, "schema-calls"),
		requests:   filepath.Join(dir, "calls.jsonl"),
		launchEnv:  filepath.Join(dir, "environment"),
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if linkErr := os.Symlink(executable, fixture.executable); linkErr != nil {
		t.Fatal(linkErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(schema), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	return fixture
}

func connectTurnProgramFixture(t *testing.T, opts *types.CodexOptions) (*Client, *Thread) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if closeErr := client.Close(closeCtx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if connectErr := client.Connect(ctx); connectErr != nil {
		t.Fatal(connectErr)
	}
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client, thread
}

func readTurnStartPrograms(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var programs []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var frame struct {
			Method string `json:"method"`
			Params struct {
				Program json.RawMessage `json:"cyberAccessProgram"`
			} `json:"params"`
		}
		if decodeErr := json.Unmarshal([]byte(line), &frame); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if frame.Method == "turn/start" {
			programs = append(programs, string(frame.Params.Program))
		}
	}
	return programs
}

func readProgramProbeCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "experimental\n")
}

func TestThreadTurnProgramRejectsRetargetedWrapper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	original := writeTurnProgramCLI(t, `{"type":"object","properties":{}}`)
	other := writeTurnProgramCLI(t, turnProgramSchema)
	dir := t.TempDir()
	target, wrapper := filepath.Join(dir, "target"), filepath.Join(dir, "launcher")
	if err := os.Symlink(original.executable, target); err != nil {
		t.Fatal(err)
	}
	writeClientExecutable(t, wrapper, fmt.Sprintf("#!/bin/sh\nexec \"$(readlink %q)\" \"$@\"\n", target))
	opts := types.NewCodexOptions().
		WithCLIPath(wrapper).
		WithEnv("GORACE=atexit_sleep_ms=0").
		WithExperimentalAPI(true)
	_, thread := connectTurnProgramFixture(t, opts)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other.executable, target); err != nil {
		t.Fatal(err)
	}
	program := types.CyberAccessProgramStandard
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("wrapper passed running executable proof: %v", err)
	}
	if started := readTurnStartPrograms(t, original.requests); len(started) != 0 {
		t.Fatalf("wrapper allowed a turn before proof: %v", started)
	}
	if calls := readProgramProbeCount(t, other.probes); calls != 0 {
		t.Fatalf("replacement target supplied %d schema proofs", calls)
	}
}

func TestTurnProgramProbeReportsCancellationOwner(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	for _, tc := range []struct {
		name string
		want error
	}{
		{"client close", types.ErrClientClosed},
		{"caller cancellation", context.Canceled},
		{"SDK deadline", ErrRuntimeControlsUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := writeTurnProgramCLI(t, turnProgramSchema)
			if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.executable), "schema-block"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			opts := types.NewCodexOptions().
				WithCLIPath(fixture.executable).
				WithEnvReplacement("GORACE=atexit_sleep_ms=0").
				WithExperimentalAPI(true)
			client, thread := connectTurnProgramFixture(t, opts)
			testCtx, cancelTest := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancelTest()
			callerCtx, cancelCaller := context.WithCancel(testCtx)
			defer cancelCaller()
			result := make(chan error, 1)
			go func() {
				program := types.CyberAccessProgramStandard
				_, err := thread.Run(callerCtx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
				result <- err
			}()
			waitForProgramProbe(t, testCtx, fixture.probes)
			switch {
			case errors.Is(tc.want, types.ErrClientClosed):
				if err := client.Close(testCtx); err != nil {
					t.Fatal(err)
				}
			case errors.Is(tc.want, context.Canceled):
				cancelCaller()
			}
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("probe error = %v, want %v", err, tc.want)
				}
				if !errors.Is(tc.want, context.Canceled) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
					t.Fatalf("SDK cancellation exposed as caller cancellation: %v", err)
				}
			case <-testCtx.Done():
				t.Fatal("probe did not return")
			}
			if started := readTurnStartPrograms(t, fixture.requests); len(started) != 0 {
				t.Fatalf("canceled proof allowed turn/start: %v", started)
			}
		})
	}
}

func waitForProgramProbe(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for readProgramProbeCount(t, path) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("probe did not reach schema generation")
		case <-ticker.C:
		}
	}
}

// A symlink to this test binary gives the SDK a direct native executable,
// so the fixture exercises the same Linux image proof as a real connection.
const turnProgramPeerName = "sdk57-turn-program-peer"

func runTurnProgramPeer() int {
	dir := filepath.Dir(os.Args[0])
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--dangerously-bypass-hook-trust" {
		args = args[1:]
	}
	if err := serveTurnProgramPeer(dir, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func serveTurnProgramPeer(dir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing test CLI command")
	}
	if err := recordTurnProgramPeerEnv(dir, args); err != nil {
		return err
	}
	switch args[0] {
	case "--version":
		_, err := fmt.Fprintln(os.Stdout, "codex 0.171.0")
		return err
	case "--help":
		_, err := fmt.Fprintln(os.Stdout, "-s, --sandbox <MODE>\n  [possible values: read-only]\n-a, --ask-for-approval <POLICY>\n  - never: no requests")
		return err
	case "app-server":
		if len(args) > 1 && args[1] == "generate-json-schema" {
			return generateTurnProgramPeerSchema(dir, args[2:])
		}
		return serveTurnProgramPeerRPC(dir)
	default:
		return fmt.Errorf("unknown test CLI command")
	}
}

func recordTurnProgramPeerEnv(dir string, args []string) error {
	arg2 := ""
	if len(args) > 1 {
		arg2 = args[1]
	}
	fields := make([]string, 0, 7)
	fields = append(fields, args[0], arg2)
	for _, key := range []string{"CODEX_HOME", "CODEX_SDK_HOOK_SOCKET", "SDK57_PUBLIC_SELECTION", "SDK57_PUBLIC_PARENT_ONLY", "HOME"} {
		value, ok := os.LookupEnv(key)
		if !ok {
			value = "unset"
		}
		fields = append(fields, value)
	}
	return appendPeerLog(filepath.Join(dir, "environment"), strings.Join(fields, "|")+"\n")
}

func generateTurnProgramPeerSchema(dir string, args []string) error {
	if len(args) == 1 && args[0] == "--help" {
		_, err := fmt.Fprintln(os.Stdout, " --out PATH\n --experimental")
		return err
	}
	if len(args) != 3 || args[0] != "--experimental" || args[1] != "--out" {
		return fmt.Errorf("test schema generator requires experimental mode")
	}
	if err := appendPeerLog(filepath.Join(dir, "schema-calls"), "experimental\n"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "schema-block")); err == nil {
		// The test cancels this probe after seeing the marker above. The
		// deadline here handles a test failure or interrupted test runner.
		time.Sleep(30 * time.Second)
	}
	body, err := os.ReadFile(filepath.Join(dir, "schema.json"))
	if err != nil {
		return err
	}
	out := filepath.Join(args[2], "v2")
	if mkdirErr := os.MkdirAll(out, 0o700); mkdirErr != nil {
		return mkdirErr
	}
	if err := os.WriteFile(filepath.Join(out, "TurnStartParams.json"), body, 0o600); err != nil {
		return err
	}
	return copyApprovedPlanPeerSchema(dir, args[2])
}

type turnProgramPeerRequest struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params struct {
		ThreadID string `json:"threadId"`
		Program  string `json:"cyberAccessProgram"`
		Markdown string `json:"markdown"`
	} `json:"params"`
}

func serveTurnProgramPeerRPC(dir string) error {
	reject, err := os.ReadFile(filepath.Join(dir, "reject-method"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	starts := 0
	for scanner.Scan() {
		line := scanner.Text()
		if err := appendPeerLog(filepath.Join(dir, "calls.jsonl"), line+"\n"); err != nil {
			return err
		}
		var frame turnProgramPeerRequest
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return err
		}
		if frame.Method == "thread/start" {
			starts++
		}
		if len(reject) > 0 && frame.Method == string(reject) {
			if _, err := fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-32123,\"message\":\"test provider rejected request\"}}\n", frame.ID); err != nil {
				return err
			}
			continue
		}
		if err := replyTurnProgramPeer(frame, starts); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func replyTurnProgramPeer(frame turnProgramPeerRequest, starts int) error {
	id := frame.ID
	switch frame.Method {
	case "initialize":
		_, err := fmt.Fprintf(os.Stdout, `{"id":%d,"result":{"userAgent":"sdk-peer","codexHome":"/fixture","platformFamily":"unix","platformOs":"linux"}}
`, id)
		return err
	case "thread/start":
		_, err := fmt.Fprintf(os.Stdout, `{"id":%d,"result":{"thread":{"id":"t%d"},"model":"fixture-model"}}
`, id, starts)
		return err
	case "thread/prepareApprovedPlan":
		return replyApprovedPlanPeer(frame)
	case "turn/start":
		if frame.Params.Program == "daybreakRed" {
			_, err := fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-32123,\"message\":\"test provider denied requested program\"}}\n", id)
			return err
		}
		_, err := fmt.Fprintf(os.Stdout, `{"method":"turn/started","params":{"threadId":%q,"turn":{"id":"turn-%d"}}}
{"id":%d,"result":{"turn":{"id":"turn-%d"}}}
{"method":"turn/completed","params":{"threadId":%q,"turn":{"id":"turn-%d","status":"completed"}}}
`, frame.Params.ThreadID, id, id, id, frame.Params.ThreadID, id)
		return err
	default:
		return nil
	}
}

func appendPeerLog(path, text string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, writeErr := file.WriteString(text); writeErr != nil {
		_ = file.Close()
		return writeErr
	}
	return file.Close()
}
