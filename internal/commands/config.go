package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadconfig"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// HookConfigGuard rejects writes to protected keys while the caller owns the hooks.
type HookConfigGuard func(callerName, keyPath string) error

func ReadConfig(ctx context.Context, send Send) (*types.Config, error) {
	resp, err := send(ctx, "ReadConfig", "config/read", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.ConfigReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadConfig: decode response: %w", err)
	}
	return &result.Config, nil
}

func ReadConfigRequirements(ctx context.Context, send Send) (*types.ConfigRequirements, error) {
	resp, err := send(ctx, "ReadConfigRequirements", "configRequirements/read", nil)
	if err != nil {
		return nil, err
	}
	var result types.ConfigRequirementsReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadConfigRequirements: decode response: %w", err)
	}
	return result.Requirements, nil
}

func WriteConfigValue(ctx context.Context, send Send, keyPath string, value any, strategy types.MergeStrategy, reject HookConfigGuard) (*types.ConfigWriteResponse, error) {
	if keyPath == "" {
		return nil, fmt.Errorf("codex.Client.WriteConfigValue: keyPath must not be empty")
	}
	if err := reject("WriteConfigValue", keyPath); err != nil {
		return nil, err
	}
	if strategy == "" {
		strategy = types.MergeReplace
	}
	resp, err := send(ctx, "WriteConfigValue", "config/value/write", types.ConfigEntry{
		KeyPath:       keyPath,
		MergeStrategy: strategy,
		Value:         value,
	})
	if err != nil {
		return nil, err
	}
	var out types.ConfigWriteResponse
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("codex.Client.WriteConfigValue: decode response: %w", err)
	}
	return &out, nil
}

func WriteConfigBatch(ctx context.Context, send Send, edits []types.ConfigEntry, reject HookConfigGuard) (*types.ConfigWriteResponse, error) {
	if len(edits) == 0 {
		return nil, fmt.Errorf("codex.Client.WriteConfigBatch: edits must not be empty")
	}
	for _, e := range edits {
		if err := reject("WriteConfigBatch", e.KeyPath); err != nil {
			return nil, err
		}
	}
	// Default mergeStrategy to MergeReplace per-edit when caller left
	// it empty. Mutates a copy so caller's slice is untouched.
	normalized := make([]types.ConfigEntry, len(edits))
	for i, e := range edits {
		if e.MergeStrategy == "" {
			e.MergeStrategy = types.MergeReplace
		}
		normalized[i] = e
	}
	resp, err := send(ctx, "WriteConfigBatch", "config/batchWrite", types.WriteConfigBatchParams{
		Edits: normalized,
	})
	if err != nil {
		return nil, err
	}
	var out types.ConfigWriteResponse
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("codex.Client.WriteConfigBatch: decode response: %w", err)
	}
	return &out, nil
}

func SetModel(ctx context.Context, send Send, model string, reject HookConfigGuard) error {
	if model == "" {
		return fmt.Errorf("codex.Client.SetModel: model must not be empty")
	}
	_, err := WriteConfigValue(ctx, send, "model", model, types.MergeReplace, reject)
	return err
}

func SetReasoningEffort(ctx context.Context, send Send, effort string, reject HookConfigGuard) error {
	if effort == "" {
		return fmt.Errorf("codex.Client.SetReasoningEffort: effort must not be empty")
	}
	_, err := WriteConfigValue(ctx, send, "model_reasoning_effort", effort, types.MergeReplace, reject)
	return err
}

func SetApprovalPolicy(ctx context.Context, send Send, policy types.ApprovalPolicy, reject HookConfigGuard, validate func(types.ApprovalPolicy) error) error {
	if policy == "" {
		return fmt.Errorf("codex.Client.SetApprovalPolicy: policy must not be empty")
	}
	if err := validate(policy); err != nil {
		return fmt.Errorf("codex.Client.SetApprovalPolicy: %w", err)
	}
	_, err := WriteConfigValue(ctx, send, "approval_policy", threadconfig.EncodeApprovalPolicy(policy), types.MergeReplace, reject)
	return err
}

func SetSandbox(ctx context.Context, send Send, sandbox types.SandboxMode, reject HookConfigGuard, overriddenError error) error {
	if sandbox == "" {
		return fmt.Errorf("codex.Client.SetSandbox: sandbox must not be empty")
	}
	result, err := WriteConfigValue(ctx, send, "sandbox_mode", string(sandbox), types.MergeReplace, reject)
	if err != nil {
		return err
	}
	switch result.Status {
	case "ok":
		return nil
	case "okOverridden":
		return fmt.Errorf("codex.Client.SetSandbox: %w", overriddenError)
	default:
		return fmt.Errorf("codex.Client.SetSandbox: unexpected config write status")
	}
}

func SetExperimentalFeature(ctx context.Context, send Send, name string, enabled bool) error {
	if name == "" {
		return fmt.Errorf("codex.Client.SetExperimentalFeature: name must not be empty")
	}
	return SetExperimentalFeatures(ctx, send, map[string]bool{name: enabled})
}

func SetExperimentalFeatures(ctx context.Context, send Send, enablement map[string]bool) error {
	if enablement == nil {
		enablement = map[string]bool{}
	}
	_, err := send(ctx, "SetExperimentalFeatures", "experimentalFeature/enablement/set",
		types.SetExperimentalFeatureParams{Enablement: enablement})
	return err
}
