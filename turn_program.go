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
	proved       bool
}

func newConnectedRuntime(cli transport.CLI, app *transport.AppServer, experimental bool) *connectedRuntime {
	return &connectedRuntime{cli: cli, app: app, experimental: experimental, probeSlot: make(chan struct{}, 1)}
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

	c.lifecycleMu.Lock()
	runtime := c.runtime
	c.lifecycleMu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("turn/start: %w", types.ErrClientClosed)
	}
	if runtime == nil || !runtime.experimental {
		return types.NewFeatureNotEnabledError("experimentalApi", "turn/start", "set WithExperimentalAPI(true) before connecting")
	}
	if !c.connected.Load() {
		return fmt.Errorf("turn/start: %w", types.ErrClientNotConnected)
	}

	probeCtx, cancel := context.WithTimeout(ctx, runtimeFeatureDiscoveryTimeout)
	defer cancel()
	stop := runtime.onClose(cancel)
	defer stop()
	probeErr := runtime.requireTurnProgramSchema(probeCtx)
	if callerErr := ctx.Err(); callerErr != nil {
		return fmt.Errorf("turn/start: check experimental schema: %w", callerErr)
	}
	if c.closed.Load() {
		return fmt.Errorf("turn/start: check experimental schema: %w", types.ErrClientClosed)
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("turn/start: experimental schema probe exceeded the SDK deadline: %w", ErrRuntimeControlsUnsupported)
	}
	return probeErr
}

func (r *connectedRuntime) requireTurnProgramSchema(ctx context.Context) error {
	select {
	case r.probeSlot <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("turn/start: check experimental schema: %w", ctx.Err())
	}
	defer func() { <-r.probeSlot }()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("turn/start: check experimental schema: %w", err)
	}
	if r.proved && r.cli.MatchesRunningExecutable(r.app.Pid()) {
		return nil
	}
	r.proved = false
	cli, release, err := r.cli.PinRunningExecutable(r.app.Pid())
	if err != nil {
		return fmt.Errorf("turn/start: verify Codex runtime executable: %w", errors.Join(ErrRuntimeControlsUnsupported, err))
	}
	defer release()
	outDir, cleanup, err := generateAppServerSchema(ctx, cli, "turn/start", true)
	if err != nil {
		return fmt.Errorf("turn/start: check cyberAccessProgram support: %w", err)
	}
	defer cleanup()
	supported, err := schemaSupportsTurnProgram(filepath.Join(outDir, "v2", "TurnStartParams.json"))
	if err != nil {
		return fmt.Errorf("turn/start: read experimental schema: %w", err)
	}
	if !supported || !r.cli.MatchesRunningExecutable(r.app.Pid()) {
		return fmt.Errorf("turn/start: connected runtime did not prove cyberAccessProgram support: %w", ErrRuntimeControlsUnsupported)
	}
	r.proved = true
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
