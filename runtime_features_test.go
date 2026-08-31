package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

func TestDiscoverRuntimeFeaturesFromSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeValidRuntimeFeatureSchemas(t, dir)

	features, err := discoverRuntimeFeaturesFromSchema(dir, true)
	if err != nil {
		t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
	}
	if !features.SubAgentActivity || !features.TurnInterrupt || !features.BackgroundTerminalInventory || !features.BackgroundTerminalTerminate || !features.BackgroundTerminalsClean {
		t.Fatalf("features = %+v", features)
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaAcceptsCodex0149SubAgentLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeValidRuntimeFeatureSchemas(t, dir)
	writeRuntimeFeatureSchema(t, dir, "v2/ItemStartedNotification.json", `{
		"type": "object",
		"definitions": {
			"SubAgentActivityKind": {"type":"string","enum":["started","interacted","interrupted"]},
			"ThreadItem": {"oneOf":[{
				"title":"SubAgentActivityThreadItem",
				"type":"object",
				"required":["agentPath","agentThreadId","id","kind","type"],
				"properties":{
					"agentPath":{"type":"string"},
					"agentThreadId":{"type":"string"},
					"id":{"type":"string"},
					"kind":{"$ref":"#/definitions/SubAgentActivityKind"},
					"type":{"type":"string","enum":["subAgentActivity"]}
				}
			}]}
		}
	}`)

	features, err := discoverRuntimeFeaturesFromSchema(dir, true)
	if err != nil {
		t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
	}
	if !features.SubAgentActivity {
		t.Fatalf("Codex 0.149.0 sub-agent lifecycle was rejected: %+v", features)
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaRejectsSubAgentLifecycleWithoutInterrupt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeValidRuntimeFeatureSchemas(t, dir)
	writeRuntimeFeatureSchema(t, dir, "v2/ItemStartedNotification.json", `{
		"type": "object",
		"definitions": {
			"SubAgentActivityKind": {"type":"string","enum":["started","interacted","completed"]},
			"ThreadItem": {"oneOf":[{
				"title":"SubAgentActivityThreadItem",
				"type":"object",
				"required":["agentPath","agentThreadId","id","kind","type"],
				"properties":{
					"agentPath":{"type":"string"},
					"agentThreadId":{"type":"string"},
					"id":{"type":"string"},
					"kind":{"$ref":"#/definitions/SubAgentActivityKind"},
					"type":{"type":"string","enum":["subAgentActivity"]}
				}
			}]}
		}
	}`)

	features, err := discoverRuntimeFeaturesFromSchema(dir, true)
	if err != nil {
		t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
	}
	if features.SubAgentActivity {
		t.Fatalf("sub-agent lifecycle without interruption was accepted: %+v", features)
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaFailsClosedPerFeature(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeRuntimeFeatureSchema(t, dir, "v2/TurnInterruptParams.json", stringSchemaWithRequired("threadId"))
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsTerminateParams.json", `{not-json`)

	features, err := discoverRuntimeFeaturesFromSchema(dir, true)
	if err == nil {
		t.Fatal("expected malformed installed schema error")
	}
	if features.TurnInterrupt || features.BackgroundTerminalTerminate {
		t.Fatalf("features = %+v", features)
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaRequiresControlResponseSchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		capability func(types.RuntimeFeatureCapabilities) bool
	}{
		{
			name:       "turn interrupt",
			path:       "v2/TurnInterruptResponse.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.TurnInterrupt },
		},
		{
			name:       "terminal cleanup",
			path:       "v2/ThreadBackgroundTerminalsCleanResponse.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalsClean },
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeValidRuntimeFeatureSchemas(t, dir)
			if err := os.Remove(filepath.Join(dir, test.path)); err != nil {
				t.Fatalf("Remove(%s): %v", test.path, err)
			}
			features, err := discoverRuntimeFeaturesFromSchema(dir, true)
			if err != nil {
				t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
			}
			if test.capability(features) {
				t.Fatalf("missing response schema advertised capability: %+v", features)
			}
		})
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaRejectsIncompatiblePropertyTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		contents   string
		capability func(types.RuntimeFeatureCapabilities) bool
	}{
		{
			name:       "subagent thread ID",
			path:       "v2/ItemStartedNotification.json",
			contents:   `{"type":"object","definitions":{"SubAgentActivityKind":{"type":"string","enum":["started","interacted","interrupted","completed"]},"ThreadItem":{"oneOf":[{"title":"SubAgentActivityThreadItem","type":"object","required":["agentPath","agentThreadId","id","kind","type"],"properties":{"agentPath":{"type":"string"},"agentThreadId":{"type":"integer"},"id":{"type":"string"},"kind":{"$ref":"#/definitions/SubAgentActivityKind"},"type":{"type":"string","enum":["subAgentActivity"]}}}]}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.SubAgentActivity },
		},
		{
			name:       "turn interrupt thread ID",
			path:       "v2/TurnInterruptParams.json",
			contents:   `{"type":"object","required":["threadId","turnId"],"properties":{"threadId":{"type":"integer"},"turnId":{"type":"string"}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.TurnInterrupt },
		},
		{
			name:       "terminal list data",
			path:       "v2/ThreadBackgroundTerminalsListResponse.json",
			contents:   `{"type":"object","required":["data"],"properties":{"data":{"type":"string"},"nextCursor":{"type":["string","null"]}},"definitions":{"LegacyAppPathString":{"type":"string"},"ThreadBackgroundTerminal":{"type":"object","required":["command","cwd","itemId","processId"],"properties":{"command":{"type":"string"},"cwd":{"$ref":"#/definitions/LegacyAppPathString"},"itemId":{"type":"string"},"processId":{"type":"string"},"osPid":{"type":["integer","null"]},"cpuPercent":{"type":["number","null"]},"rssKb":{"type":["integer","null"]}}}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalInventory },
		},
		{
			name:       "terminal process ID",
			path:       "v2/ThreadBackgroundTerminalsTerminateParams.json",
			contents:   `{"type":"object","required":["threadId","processId"],"properties":{"threadId":{"type":"string"},"processId":{"type":"integer"}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalTerminate },
		},
		{
			name:       "terminal termination acknowledgement",
			path:       "v2/ThreadBackgroundTerminalsTerminateResponse.json",
			contents:   `{"type":"object","required":["terminated"],"properties":{"terminated":{"type":"string"}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalTerminate },
		},
		{
			name:       "terminal cleanup thread ID",
			path:       "v2/ThreadBackgroundTerminalsCleanParams.json",
			contents:   `{"type":"object","required":["threadId"],"properties":{"threadId":{"type":"integer"}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalsClean },
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeValidRuntimeFeatureSchemas(t, dir)
			writeRuntimeFeatureSchema(t, dir, test.path, test.contents)
			features, err := discoverRuntimeFeaturesFromSchema(dir, true)
			if err != nil {
				t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
			}
			if test.capability(features) {
				t.Fatalf("incompatible schema advertised capability: %+v", features)
			}
		})
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaRejectsNonObjectRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		capability func(types.RuntimeFeatureCapabilities) bool
	}{
		{
			name:       "subagent activity notification",
			path:       "v2/ItemStartedNotification.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.SubAgentActivity },
		},
		{
			name:       "turn interrupt request",
			path:       "v2/TurnInterruptParams.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.TurnInterrupt },
		},
		{
			name:       "turn interrupt response",
			path:       "v2/TurnInterruptResponse.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.TurnInterrupt },
		},
		{
			name:       "terminal inventory request",
			path:       "v2/ThreadBackgroundTerminalsListParams.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalInventory },
		},
		{
			name:       "terminal inventory response",
			path:       "v2/ThreadBackgroundTerminalsListResponse.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalInventory },
		},
		{
			name:       "terminal terminate request",
			path:       "v2/ThreadBackgroundTerminalsTerminateParams.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalTerminate },
		},
		{
			name:       "terminal terminate response",
			path:       "v2/ThreadBackgroundTerminalsTerminateResponse.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalTerminate },
		},
		{
			name:       "terminal cleanup request",
			path:       "v2/ThreadBackgroundTerminalsCleanParams.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalsClean },
		},
		{
			name:       "terminal cleanup response",
			path:       "v2/ThreadBackgroundTerminalsCleanResponse.json",
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalsClean },
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeValidRuntimeFeatureSchemas(t, dir)
			writeRuntimeFeatureSchemaRootType(t, dir, test.path, "array")

			features, err := discoverRuntimeFeaturesFromSchema(dir, true)
			if err != nil {
				t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
			}
			if test.capability(features) {
				t.Fatalf("non-object schema advertised capability: %+v", features)
			}
		})
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaRejectsUnknownRequiredRequestFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		contents   string
		capability func(types.RuntimeFeatureCapabilities) bool
	}{
		{
			name:       "turn interrupt",
			path:       "v2/TurnInterruptParams.json",
			contents:   stringSchemaWithRequired("threadId", "turnId", "reason"),
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.TurnInterrupt },
		},
		{
			name:       "terminal inventory",
			path:       "v2/ThreadBackgroundTerminalsListParams.json",
			contents:   `{"type":"object","required":["threadId","scope"],"properties":{"threadId":{"type":"string"},"cursor":{"type":["string","null"]},"limit":{"type":["integer","null"]},"scope":{"type":"string"}}}`,
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalInventory },
		},
		{
			name:       "terminal terminate",
			path:       "v2/ThreadBackgroundTerminalsTerminateParams.json",
			contents:   stringSchemaWithRequired("threadId", "processId", "force"),
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalTerminate },
		},
		{
			name:       "terminal clean",
			path:       "v2/ThreadBackgroundTerminalsCleanParams.json",
			contents:   stringSchemaWithRequired("threadId", "reason"),
			capability: func(features types.RuntimeFeatureCapabilities) bool { return features.BackgroundTerminalsClean },
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeValidRuntimeFeatureSchemas(t, dir)
			writeRuntimeFeatureSchema(t, dir, test.path, test.contents)
			features, err := discoverRuntimeFeaturesFromSchema(dir, true)
			if err != nil {
				t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
			}
			if test.capability(features) {
				t.Fatalf("schema with unsupported required field advertised capability: %+v", features)
			}
		})
	}
}

func TestDiscoverRuntimeFeaturesFromSchemaAllowsUnknownRequiredResponseFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeValidRuntimeFeatureSchemas(t, dir)
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsTerminateResponse.json", `{
		"type":"object",
		"required":["terminated","serverVersion"],
		"properties":{"terminated":{"type":"boolean"},"serverVersion":{"type":"string"}}
	}`)
	features, err := discoverRuntimeFeaturesFromSchema(dir, true)
	if err != nil {
		t.Fatalf("discoverRuntimeFeaturesFromSchema: %v", err)
	}
	if !features.BackgroundTerminalTerminate {
		t.Fatalf("forward-compatible response field disabled capability: %+v", features)
	}
}

func stringSchemaWithRequired(fields ...string) string {
	required := ""
	properties := ""
	for i, field := range fields {
		if i > 0 {
			required += ","
			properties += ","
		}
		required += `"` + field + `"`
		properties += `"` + field + `":{"type":"string"}`
	}
	return `{"type":"object","required":[` + required + `],"properties":{` + properties + `}}`
}

func writeValidRuntimeFeatureSchemas(t *testing.T, dir string) {
	t.Helper()
	writeRuntimeFeatureSchema(t, dir, "v2/ItemStartedNotification.json", `{
		"type": "object",
		"definitions": {
			"SubAgentActivityKind": {"type":"string","enum":["started","interacted","interrupted","completed"]},
			"ThreadItem": {"oneOf":[{
				"title":"SubAgentActivityThreadItem",
				"type":"object",
				"required":["agentPath","agentThreadId","id","kind","type"],
				"properties":{
					"agentPath":{"type":"string"},
					"agentThreadId":{"type":"string"},
					"id":{"type":"string"},
					"kind":{"$ref":"#/definitions/SubAgentActivityKind"},
					"type":{"type":"string","enum":["subAgentActivity"]}
				}
			}]}
		}
	}`)
	writeRuntimeFeatureSchema(t, dir, "v2/TurnInterruptParams.json", stringSchemaWithRequired("threadId", "turnId"))
	writeRuntimeFeatureSchema(t, dir, "v2/TurnInterruptResponse.json", `{"type":"object"}`)
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsListParams.json", `{
		"type":"object",
		"required":["threadId"],
		"properties":{
			"threadId":{"type":"string"},
			"cursor":{"type":["string","null"]},
			"limit":{"type":["integer","null"]}
		}
	}`)
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsListResponse.json", `{
		"type":"object",
		"required":["data"],
		"properties":{
			"data":{"type":"array","items":{"$ref":"#/definitions/ThreadBackgroundTerminal"}},
			"nextCursor":{"type":["string","null"]}
		},
		"definitions":{
			"LegacyAppPathString":{"type":"string"},
			"ThreadBackgroundTerminal":{
				"type":"object",
				"required":["command","cwd","itemId","processId"],
				"properties":{
					"command":{"type":"string"},
					"cwd":{"$ref":"#/definitions/LegacyAppPathString"},
					"itemId":{"type":"string"},
					"processId":{"type":"string"},
					"osPid":{"type":["integer","null"]},
					"cpuPercent":{"type":["number","null"]},
					"rssKb":{"type":["integer","null"]}
				}
			}
		}
	}`)
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsTerminateParams.json", stringSchemaWithRequired("threadId", "processId"))
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsTerminateResponse.json", `{"type":"object","required":["terminated"],"properties":{"terminated":{"type":"boolean"}}}`)
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsCleanParams.json", stringSchemaWithRequired("threadId"))
	writeRuntimeFeatureSchema(t, dir, "v2/ThreadBackgroundTerminalsCleanResponse.json", `{"type":"object"}`)
}

func writeRuntimeFeatureSchemaRootType(t *testing.T, root, name, schemaType string) {
	t.Helper()
	path := filepath.Join(root, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	var schema map[string]any
	if unmarshalErr := json.Unmarshal(raw, &schema); unmarshalErr != nil {
		t.Fatalf("Unmarshal(%s): %v", name, unmarshalErr)
	}
	schema["type"] = schemaType
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal(%s): %v", name, err)
	}
	writeRuntimeFeatureSchema(t, root, name, string(encoded))
}

func writeRuntimeFeatureSchema(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
