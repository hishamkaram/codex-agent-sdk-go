package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// connectedRuntime pins the launch inputs and actual handshake opt-in. It
// belongs to one Client connection. The probe slot protects the schema proof.
type connectedRuntime struct {
	cli          transport.CLI
	app          *transport.AppServer
	experimental bool
	onClose      func(func()) func() bool // bound before the connection is published
	probeSlot    chan struct{}
	proved       map[string]struct{}
}

func newConnectedRuntime(cli transport.CLI, app *transport.AppServer, experimental bool) *connectedRuntime {
	return &connectedRuntime{cli: cli, app: app, experimental: experimental, probeSlot: make(chan struct{}, 1), proved: make(map[string]struct{})}
}

func (c *Client) requireTurnProgram(ctx context.Context, program types.CyberAccessProgram) error {
	if err := c.checkApprovalReentry(ctx); err != nil {
		return fmt.Errorf("turn/start: %w", err)
	}
	switch program {
	case types.CyberAccessProgramStandard, types.CyberAccessProgramDaybreakBlue, types.CyberAccessProgramDaybreakRed:
	default:
		return fmt.Errorf("turn/start: invalid cyberAccessProgram")
	}

	return c.requireConnectedSchema(ctx, connectedSchemaFeature{
		method: "turn/start", name: "cyberAccessProgram",
		supported: func(root string) (bool, error) {
			return schemaSupportsTurnProgram(filepath.Join(root, "v2", "TurnStartParams.json"))
		},
	})
}

type connectedSchemaFeature struct {
	method    string
	name      string
	supported func(root string) (bool, error)
}

func (c *Client) requireConnectedSchema(ctx context.Context, feature connectedSchemaFeature) error {
	c.lifecycleMu.Lock()
	runtime := c.runtime
	c.lifecycleMu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("%s: %w", feature.method, types.ErrClientClosed)
	}
	if runtime == nil || !runtime.experimental {
		return types.NewFeatureNotEnabledError("experimentalApi", feature.method, "set WithExperimentalAPI(true) before connecting")
	}
	if !c.connected.Load() {
		return fmt.Errorf("%s: %w", feature.method, types.ErrClientNotConnected)
	}

	probeCtx, cancel := context.WithTimeout(ctx, runtimeFeatureDiscoveryTimeout)
	defer cancel()
	stop := runtime.onClose(cancel)
	defer stop()
	probeErr := runtime.requireSchema(probeCtx, feature)
	if callerErr := ctx.Err(); callerErr != nil {
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, callerErr)
	}
	if c.closed.Load() {
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, types.ErrClientClosed)
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s: experimental schema probe exceeded the SDK deadline: %w", feature.method, ErrRuntimeControlsUnsupported)
	}
	return probeErr
}

func (r *connectedRuntime) requireSchema(ctx context.Context, feature connectedSchemaFeature) error {
	select {
	case r.probeSlot <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, ctx.Err())
	}
	defer func() { <-r.probeSlot }()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: check experimental schema: %w", feature.method, err)
	}
	if _, proved := r.proved[feature.name]; proved {
		if r.cli.MatchesRunningExecutable(r.app.Pid()) {
			return nil
		}
		clear(r.proved)
	}
	cli, release, err := r.cli.PinRunningExecutable(r.app.Pid())
	if err != nil {
		return fmt.Errorf("%s: verify Codex runtime executable: %w", feature.method, errors.Join(ErrRuntimeControlsUnsupported, err))
	}
	defer release()
	outDir, cleanup, err := generateAppServerSchema(ctx, cli, feature.method, true)
	if err != nil {
		return fmt.Errorf("%s: check %s support: %w", feature.method, feature.name, err)
	}
	defer cleanup()
	supported, err := feature.supported(outDir)
	if err != nil {
		return fmt.Errorf("%s: read experimental schema: %w", feature.method, err)
	}
	if !supported || !r.cli.MatchesRunningExecutable(r.app.Pid()) {
		return fmt.Errorf("%s: connected runtime did not prove %s support: %w", feature.method, feature.name, ErrRuntimeControlsUnsupported)
	}
	r.proved[feature.name] = struct{}{}
	return nil
}

func schemaSupportsTurnProgram(path string) (bool, error) {
	var schema struct {
		runtimeObjectSchema
		Definitions map[string]runtimePropertySchema `json:"definitions"`
	}
	found, err := readRuntimeSchema(path, &schema)
	if err != nil || !found {
		return false, err
	}
	raw, ok := schema.Properties["cyberAccessProgram"]
	if !ok || !schemaTypesEqual(schema.Type, "object") || slices.Contains(schema.Required, "cyberAccessProgram") {
		return false, nil
	}
	var property struct {
		AnyOf []runtimePropertySchema `json:"anyOf"`
	}
	if decodeErr := json.Unmarshal(raw, &property); decodeErr != nil {
		return false, decodeErr
	}
	enum := schema.Definitions["CyberAccessProgram"]
	supported := schemaTypesEqual(enum.Type, "string") &&
		equalStringSet(enum.Enum, []string{
			string(types.CyberAccessProgramStandard),
			string(types.CyberAccessProgramDaybreakBlue),
			string(types.CyberAccessProgramDaybreakRed),
		}) && hasTurnProgramReference(property.AnyOf)
	return supported, nil
}

func hasTurnProgramReference(variants []runtimePropertySchema) bool {
	if len(variants) != 2 {
		return false
	}
	first, second := variants[0], variants[1]
	if schemaTypesEqual(first.Type, "null") {
		first, second = second, first
	}
	return first.Ref == "#/definitions/CyberAccessProgram" && len(first.Type) == 0 && len(first.Enum) == 0 &&
		second.Ref == "" && len(second.Enum) == 0 && schemaTypesEqual(second.Type, "null")
}
