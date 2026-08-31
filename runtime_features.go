package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const runtimeFeatureDiscoveryTimeout = 5 * time.Second

// DiscoverRuntimeFeatures proves optional app-server surfaces from generated
// schemas. Missing schemas disable only the corresponding feature; malformed
// schemas return an error so callers fail closed instead of advertising stale
// controls.
func DiscoverRuntimeFeatures(ctx context.Context, options *types.CodexOptions) (types.RuntimeFeatureCapabilities, error) {
	return discoverRuntimeFeatures(ctx, options, runtimeFeatureDiscoveryTimeout)
}

func discoverRuntimeFeatures(
	ctx context.Context,
	options *types.CodexOptions,
	timeout time.Duration,
) (types.RuntimeFeatureCapabilities, error) {
	if ctx == nil {
		return types.RuntimeFeatureCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeFeatures: context is required")
	}
	if options == nil {
		options = types.NewCodexOptions()
	}
	cliPath := strings.TrimSpace(options.CLIPath)
	if cliPath == "" {
		var err error
		cliPath, err = transport.FindCLI()
		if err != nil {
			return types.RuntimeFeatureCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeFeatures: find CLI: %w", err)
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	features := types.RuntimeFeatureCapabilities{}
	version, err := transport.ProbeCLIVersionContext(probeCtx, cliPath, options.Env)
	if err == nil {
		features.CLIVersion = version.String()
	} else if ctxErr := probeCtx.Err(); ctxErr != nil {
		return types.RuntimeFeatureCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeFeatures: CLI version: %w", ctxErr)
	}
	outDir, cleanup, experimentalSchema, err := generateRuntimeFeatureSchema(
		probeCtx,
		cliPath,
		options.Env,
		options.ExperimentalAPI,
	)
	if err != nil {
		if errors.Is(err, ErrRuntimeControlsUnsupported) {
			return features, nil
		}
		return types.RuntimeFeatureCapabilities{}, err
	}
	defer cleanup()
	discovered, err := discoverRuntimeFeaturesFromSchema(outDir, experimentalSchema)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, fmt.Errorf("codex.DiscoverRuntimeFeatures: %w", err)
	}
	discovered.CLIVersion = features.CLIVersion
	return discovered, nil
}

func generateRuntimeFeatureSchema(
	ctx context.Context,
	cliPath string,
	env []string,
	experimental bool,
) (outDir string, cleanup func(), experimentalSchema bool, err error) {
	const caller = "codex.DiscoverRuntimeFeatures"
	outDir, cleanup, err = generateAppServerSchema(ctx, cliPath, env, caller, experimental)
	if err == nil || !experimental || !errors.Is(err, ErrRuntimeControlsUnsupported) {
		return outDir, cleanup, experimental, err
	}
	outDir, cleanup, err = generateAppServerSchema(ctx, cliPath, env, caller, false)
	return outDir, cleanup, false, err
}

func discoverRuntimeFeaturesFromSchema(root string, includeExperimental bool) (types.RuntimeFeatureCapabilities, error) {
	var features types.RuntimeFeatureCapabilities
	var err error
	if features.SubAgentActivity, err = schemaHasSubAgentActivity(filepath.Join(root, "v2", "ItemStartedNotification.json")); err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	turnInterruptParams, err := schemaHasRequestProperties(
		filepath.Join(root, "v2", "TurnInterruptParams.json"),
		requiredTypedProperty("threadId", "string"),
		requiredTypedProperty("turnId", "string"),
	)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	turnInterruptResponse, err := schemaHasProperties(filepath.Join(root, "v2", "TurnInterruptResponse.json"))
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	features.TurnInterrupt = turnInterruptParams && turnInterruptResponse
	if !includeExperimental {
		return features, nil
	}

	listParams, err := schemaHasRequestProperties(
		filepath.Join(root, "v2", "ThreadBackgroundTerminalsListParams.json"),
		requiredTypedProperty("threadId", "string"),
		optionalTypedProperty("cursor", "string", "null"),
		optionalTypedProperty("limit", "integer", "null"),
	)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	listResponse, err := schemaHasTerminalListResponse(filepath.Join(root, "v2", "ThreadBackgroundTerminalsListResponse.json"))
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	features.BackgroundTerminalInventory = listParams && listResponse

	terminateParams, err := schemaHasRequestProperties(
		filepath.Join(root, "v2", "ThreadBackgroundTerminalsTerminateParams.json"),
		requiredTypedProperty("threadId", "string"),
		requiredTypedProperty("processId", "string"),
	)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	terminateResponse, err := schemaHasProperties(
		filepath.Join(root, "v2", "ThreadBackgroundTerminalsTerminateResponse.json"),
		requiredTypedProperty("terminated", "boolean"),
	)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	features.BackgroundTerminalTerminate = terminateParams && terminateResponse

	cleanParams, err := schemaHasRequestProperties(
		filepath.Join(root, "v2", "ThreadBackgroundTerminalsCleanParams.json"),
		requiredTypedProperty("threadId", "string"),
	)
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	cleanResponse, err := schemaHasProperties(filepath.Join(root, "v2", "ThreadBackgroundTerminalsCleanResponse.json"))
	if err != nil {
		return types.RuntimeFeatureCapabilities{}, err
	}
	features.BackgroundTerminalsClean = cleanParams && cleanResponse
	return features, nil
}

type runtimeObjectSchema struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
	Type       json.RawMessage            `json:"type"`
}

type runtimePropertySchema struct {
	Type  json.RawMessage        `json:"type"`
	Ref   string                 `json:"$ref"`
	Items *runtimePropertySchema `json:"items"`
	Enum  []string               `json:"enum"`
}

type runtimePropertyExpectation struct {
	name     string
	required bool
	types    []string
	ref      string
	itemsRef string
	enum     []string
}

func requiredTypedProperty(name string, schemaTypes ...string) runtimePropertyExpectation {
	return runtimePropertyExpectation{name: name, required: true, types: schemaTypes}
}

func optionalTypedProperty(name string, schemaTypes ...string) runtimePropertyExpectation {
	return runtimePropertyExpectation{name: name, types: schemaTypes}
}

func schemaHasProperties(path string, properties ...runtimePropertyExpectation) (bool, error) {
	var schema runtimeObjectSchema
	found, err := readRuntimeSchema(path, &schema)
	if err != nil || !found {
		return false, err
	}
	return objectSchemaMatches(schema, properties...), nil
}

func schemaHasRequestProperties(path string, properties ...runtimePropertyExpectation) (bool, error) {
	var schema runtimeObjectSchema
	found, err := readRuntimeSchema(path, &schema)
	if err != nil || !found {
		return false, err
	}
	if !objectSchemaMatches(schema, properties...) {
		return false, nil
	}
	expectedRequired := make([]string, 0, len(properties))
	for _, property := range properties {
		if property.required {
			expectedRequired = append(expectedRequired, property.name)
		}
	}
	return equalStringSet(schema.Required, expectedRequired), nil
}

func schemaHasTerminalListResponse(path string) (bool, error) {
	var schema struct {
		runtimeObjectSchema
		Definitions map[string]runtimeObjectSchema `json:"definitions"`
	}
	found, err := readRuntimeSchema(path, &schema)
	if err != nil || !found {
		return false, err
	}
	terminal, terminalOK := schema.Definitions["ThreadBackgroundTerminal"]
	legacyPath, pathOK := schema.Definitions["LegacyAppPathString"]
	return objectSchemaMatches(
		schema.runtimeObjectSchema,
		runtimePropertyExpectation{
			name:     "data",
			required: true,
			types:    []string{"array"},
			itemsRef: "#/definitions/ThreadBackgroundTerminal",
		},
		optionalTypedProperty("nextCursor", "string", "null"),
	) && terminalOK && schemaTypesEqual(terminal.Type, "object") &&
		objectSchemaMatches(
			terminal,
			requiredTypedProperty("command", "string"),
			runtimePropertyExpectation{name: "cwd", required: true, ref: "#/definitions/LegacyAppPathString"},
			requiredTypedProperty("itemId", "string"),
			requiredTypedProperty("processId", "string"),
			optionalTypedProperty("osPid", "integer", "null"),
			optionalTypedProperty("cpuPercent", "number", "null"),
			optionalTypedProperty("rssKb", "integer", "null"),
		) && pathOK && schemaTypesEqual(legacyPath.Type, "string"), nil
}

func schemaHasSubAgentActivity(path string) (bool, error) {
	var schema struct {
		Type        json.RawMessage `json:"type"`
		Definitions struct {
			Kind struct {
				Type json.RawMessage `json:"type"`
				Enum []string        `json:"enum"`
			} `json:"SubAgentActivityKind"`
			ThreadItem struct {
				OneOf []struct {
					Title      string                     `json:"title"`
					Type       json.RawMessage            `json:"type"`
					Required   []string                   `json:"required"`
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"oneOf"`
			} `json:"ThreadItem"`
		} `json:"definitions"`
	}
	found, err := readRuntimeSchema(path, &schema)
	if err != nil || !found {
		return false, err
	}
	if !schemaTypesEqual(schema.Type, "object") ||
		!schemaTypesEqual(schema.Definitions.Kind.Type, "string") ||
		!containsAllStrings(schema.Definitions.Kind.Enum, "started", "interrupted") {
		return false, nil
	}
	for _, variant := range schema.Definitions.ThreadItem.OneOf {
		if variant.Title != "SubAgentActivityThreadItem" {
			continue
		}
		if schemaTypesEqual(variant.Type, "object") && objectSchemaMatches(
			runtimeObjectSchema{Required: variant.Required, Properties: variant.Properties, Type: variant.Type},
			requiredTypedProperty("agentPath", "string"),
			requiredTypedProperty("agentThreadId", "string"),
			requiredTypedProperty("id", "string"),
			runtimePropertyExpectation{name: "kind", required: true, ref: "#/definitions/SubAgentActivityKind"},
			runtimePropertyExpectation{name: "type", required: true, types: []string{"string"}, enum: []string{"subAgentActivity"}},
		) {
			return true, nil
		}
	}
	return false, nil
}

func readRuntimeSchema(path string, target any) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read schema %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return false, fmt.Errorf("decode schema %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

func objectSchemaMatches(schema runtimeObjectSchema, properties ...runtimePropertyExpectation) bool {
	if !schemaTypesEqual(schema.Type, "object") {
		return false
	}
	required := make(map[string]struct{}, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = struct{}{}
	}
	for _, expected := range properties {
		_, isRequired := required[expected.name]
		if isRequired != expected.required {
			return false
		}
		raw, ok := schema.Properties[expected.name]
		if !ok || !propertySchemaMatches(raw, expected) {
			return false
		}
	}
	return true
}

func propertySchemaMatches(raw json.RawMessage, expected runtimePropertyExpectation) bool {
	var property runtimePropertySchema
	if err := json.Unmarshal(raw, &property); err != nil {
		return false
	}
	if len(expected.types) > 0 && !schemaTypesEqual(property.Type, expected.types...) {
		return false
	}
	if expected.ref != "" && property.Ref != expected.ref {
		return false
	}
	if expected.itemsRef != "" && (property.Items == nil || property.Items.Ref != expected.itemsRef) {
		return false
	}
	return len(expected.enum) == 0 || equalStringSet(property.Enum, expected.enum)
}

func schemaTypesEqual(raw json.RawMessage, expected ...string) bool {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return equalStringSet([]string{single}, expected)
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return false
	}
	return equalStringSet(multiple, expected)
}

func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	set := make(map[string]struct{}, len(left))
	for _, value := range left {
		set[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func containsAllStrings(values []string, required ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}
