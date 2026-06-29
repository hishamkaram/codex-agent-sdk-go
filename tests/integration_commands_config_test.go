//go:build integration

package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestIntCmd_WriteConfigValue_RoundTrip(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)

	// Read current model, write it back (semantic no-op, byte-protected).
	cfg, err := c.ReadConfig(context.Background())
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	model := "gpt-5.4"
	if cfg.Model != nil && *cfg.Model != "" {
		model = *cfg.Model
	}
	resp, err := c.WriteConfigValue(context.Background(), "model", model, types.MergeReplace)
	if err != nil {
		t.Fatalf("WriteConfigValue: %v", err)
	}
	if resp == nil || resp.Status == "" {
		t.Errorf("expected ConfigWriteResponse with Status populated, got %+v", resp)
	}
	t.Logf("round-tripped model=%q status=%q", model, resp.Status)
}

func TestIntCmd_WriteConfigValue_UnknownKey(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	// Codex 0.121.0 may either silently accept or reject unknown
	// top-level keys. The test asserts the behavior is deterministic
	// (no panic) — actual semantics are documented from the response.
	_, err := c.WriteConfigValue(context.Background(), "_v040_test_marker_does_not_exist", "x", types.MergeReplace)
	if err != nil {
		t.Logf("unknown-key write rejected (expected): %v", err)
		return
	}
	t.Logf("unknown-key write silently accepted by codex 0.121.0")
}

func TestIntCmd_WriteConfigValue_EmptyKeyPath(t *testing.T) {
	c := connectReadOnlyClient(t)
	_, err := c.WriteConfigValue(context.Background(), "", "x", types.MergeReplace)
	if err == nil {
		t.Fatal("expected error for empty keyPath")
	}
	if !strings.Contains(err.Error(), "keyPath must not be empty") {
		t.Errorf("err = %q, want 'keyPath must not be empty'", err)
	}
}

func TestIntCmd_SetModel_RoundTrip(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	cfg, _ := c.ReadConfig(context.Background())
	model := "gpt-5.4"
	if cfg != nil && cfg.Model != nil && *cfg.Model != "" {
		model = *cfg.Model
	}
	if err := c.SetModel(context.Background(), model); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
}

func TestIntCmd_SetApprovalPolicy_RoundTrip(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	if err := c.SetApprovalPolicy(context.Background(), types.ApprovalOnRequest); err != nil {
		t.Fatalf("SetApprovalPolicy: %v", err)
	}
}

func TestIntCmd_SetSandbox_RoundTrip(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	if err := c.SetSandbox(context.Background(), types.SandboxReadOnly); err != nil {
		t.Fatalf("SetSandbox: %v", err)
	}
}

func TestIntCmd_WriteConfigBatch_Happy(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	cfg, _ := c.ReadConfig(context.Background())
	model := "gpt-5.4"
	if cfg != nil && cfg.Model != nil && *cfg.Model != "" {
		model = *cfg.Model
	}
	resp, err := c.WriteConfigBatch(context.Background(), []types.ConfigEntry{
		{KeyPath: "model", Value: model}, // mergeStrategy defaulted by SDK
		{KeyPath: "approval_policy", Value: "on-request", MergeStrategy: types.MergeReplace},
	})
	if err != nil {
		t.Fatalf("WriteConfigBatch: %v", err)
	}
	if resp == nil || resp.Status == "" {
		t.Errorf("expected ConfigWriteResponse with Status populated, got %+v", resp)
	}
}

func TestIntCmd_WriteConfigBatch_EmptyEdits(t *testing.T) {
	c := connectReadOnlyClient(t)
	_, err := c.WriteConfigBatch(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty edits")
	}
	if !strings.Contains(err.Error(), "edits must not be empty") {
		t.Errorf("err = %q", err)
	}
}

// ====================================================================
// SetExperimentalFeature (mutating — safety net)
// ====================================================================

func TestIntCmd_SetExperimentalFeature_ToggleOnOff(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	// codex 0.121.0 supports SetExperimentalFeature only for a small
	// set of "runtime-toggleable" features (the rest must be edited
	// in config.toml). Probed live error message:
	//   "currently supported features are apps, plugins, tool_search,
	//    tool_suggest, tool_call_mcp_elicitation"
	// Pick tool_search — defaults to false, can be toggled and
	// restored without disturbing user-visible behavior.
	feats, err := c.ListExperimentalFeatures(context.Background())
	if err != nil {
		t.Fatalf("ListExperimentalFeatures: %v", err)
	}
	var target *types.ExperimentalFeature
	for i, f := range feats {
		if f.Name == "tool_search" {
			target = &feats[i]
			break
		}
	}
	if target == nil {
		t.Skip("tool_search feature not present; codex schema may have changed")
	}
	original := target.Enabled
	if err := c.SetExperimentalFeature(context.Background(), target.Name, !original); err != nil {
		t.Fatalf("SetExperimentalFeature toggle: %v", err)
	}
	if err := c.SetExperimentalFeature(context.Background(), target.Name, original); err != nil {
		t.Fatalf("SetExperimentalFeature restore: %v", err)
	}
	t.Logf("toggled %q on/off cleanly", target.Name)
}

func TestIntCmd_SetExperimentalFeature_UnsupportedFeature(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	// shell_tool exists in ListExperimentalFeatures but is NOT
	// runtime-toggleable. Codex returns a structured RPC error.
	err := c.SetExperimentalFeature(context.Background(), "shell_tool", false)
	if err == nil {
		t.Fatal("expected RPC error for non-toggleable feature")
	}
	if !types.IsRPCError(err) {
		t.Errorf("expected RPCError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "unsupported feature enablement") {
		t.Errorf("err = %q, want 'unsupported feature enablement'", err)
	}
}

func TestIntCmd_SetExperimentalFeatures_Bulk(t *testing.T) {
	safetyNetCodexConfig(t)
	c := connectReadOnlyClient(t)
	// Toggle two features at once, then restore.
	feats, _ := c.ListExperimentalFeatures(context.Background())
	original := map[string]bool{}
	updates := map[string]bool{}
	for _, f := range feats {
		if f.Name == "tool_search" || f.Name == "tool_suggest" {
			original[f.Name] = f.Enabled
			updates[f.Name] = !f.Enabled
		}
	}
	if len(updates) < 2 {
		t.Skip("tool_search + tool_suggest not both present in this codex version")
	}
	if err := c.SetExperimentalFeatures(context.Background(), updates); err != nil {
		t.Fatalf("bulk toggle: %v", err)
	}
	if err := c.SetExperimentalFeatures(context.Background(), original); err != nil {
		t.Fatalf("bulk restore: %v", err)
	}
}

func TestIntCmd_SetExperimentalFeatures_EmptyMap(t *testing.T) {
	c := connectReadOnlyClient(t)
	// Empty map is documented as a no-op probe — must NOT error.
	if err := c.SetExperimentalFeatures(context.Background(), nil); err != nil {
		t.Fatalf("empty enablement should be a no-op, got: %v", err)
	}
}

func TestIntCmd_SetExperimentalFeature_EmptyName(t *testing.T) {
	c := connectReadOnlyClient(t)
	err := c.SetExperimentalFeature(context.Background(), "", true)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// UploadFeedback (sends to OpenAI servers — opt-in only)
// ====================================================================
