package codex

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
