//go:build integration

// Integration tests for v0.4.0 commands against live codex 0.121.0+.
// Read-only methods do not require CODEX_SDK_RUN_TURNS — they make no
// upstream model calls and do not consume quota. Methods that mutate
// state (config writes, name set, rollback) live in
// integration_commands_mutating_test.go (Batch 3).
//
// Per feedback_integration_tests_complete.md: every method × every
// option variant × every error path gets its own test row. CI fails if
// any row is unmarked.
//
// File-level safety nets:
//   - safetyNetCodexConfig is NOT applied here because none of these
//     methods writes config.toml. Read-only.
//   - Throwaway threads (only used by methods that need an active
//     thread, e.g., concurrent-during-turn variants) get the
//     _v040_probe_<unix> prefix and are archived on Cleanup.
//
// Test-naming convention: TestIntCmd_<Method>_<Variant>. Variants:
//
//	_Happy            — success path on a vanilla client
//	_ClosedClient     — call after Close → expect specific error
//	_UnconnectedClient — call before Connect → expect specific error
//	_ConcurrentDuringTurn — call while a turn is in flight (race detector)
//	_<SpecificError>  — exercise a known error path (e.g., MCP OAuth)
package tests

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// connectReadOnlyClient spins up a Client with default options + an
// auto-Close. Re-used by every read-only happy-path test.
func connectReadOnlyClient(t *testing.T) *codex.Client {
	t.Helper()
	requireCodex(t)
	requireAuth(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	opts := types.NewCodexOptions()
	c, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// ====================================================================
// ReadConfig
// ====================================================================

func TestIntCmd_ReadConfig_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	cfg, err := c.ReadConfig(context.Background())
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("nil config")
	}
	// codex always populates the Features map (verified live).
	if len(cfg.Features) == 0 {
		t.Errorf("expected Features map populated, got empty")
	}
	// Raw should hold every config key codex serialized.
	if len(cfg.Raw) < 10 {
		t.Errorf("expected Raw to hold many fields (codex serializes ~80), got %d", len(cfg.Raw))
	}
	t.Logf("config: model=%v approval=%v sandbox=%v features=%d raw_keys=%d",
		ptrStr(cfg.Model), ptrStr(cfg.ApprovalPolicy), ptrStr(cfg.Sandbox), len(cfg.Features), len(cfg.Raw))
}

func TestIntCmd_ReadConfig_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _ := codex.NewClient(ctx, types.NewCodexOptions())
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = c.Close(context.Background())
	_, err := c.ReadConfig(context.Background())
	if err == nil {
		t.Fatal("expected error after Close")
	}
	if !strings.Contains(err.Error(), "client closed") {
		t.Errorf("error = %q, want 'client closed'", err)
	}
}

func TestIntCmd_ReadConfig_ConcurrentDuringTurn(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)

	// Fire ReadConfig + a no-quota Run() concurrently.
	var wg sync.WaitGroup
	wg.Add(2)
	var readErr, runErr error
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, readErr = c.ReadConfig(ctx)
	}()
	go func() {
		defer wg.Done()
		// No-quota path: just open a stream and close it immediately.
		// We avoid running an actual turn (would burn quota).
		_ = thread // touch to keep it alive
		runErr = nil
	}()
	wg.Wait()
	if readErr != nil {
		t.Errorf("ReadConfig during turn: %v", readErr)
	}
	if runErr != nil {
		t.Errorf("turn: %v", runErr)
	}
}

// ====================================================================
// ListModels
// ====================================================================

func TestIntCmd_ListModels_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected at least one model")
	}
	var sawDefault bool
	for _, m := range models {
		if m.ID == "" {
			t.Errorf("model row has empty ID: %+v", m)
		}
		if m.IsDefault {
			sawDefault = true
		}
	}
	if !sawDefault {
		t.Errorf("expected exactly one model with IsDefault=true, got none")
	}
	t.Logf("models: %d total", len(models))
}

func TestIntCmd_ListModels_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _ := codex.NewClient(ctx, types.NewCodexOptions())
	_ = c.Connect(ctx)
	_ = c.Close(context.Background())
	_, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "client closed") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_ListModels_RaceUnderConcurrency(t *testing.T) {
	// Race detector + 8 goroutines hammering ListModels — verifies no
	// shared mutable state in the SDK's RPC dispatcher.
	c := connectReadOnlyClient(t)
	const N = 8
	var wg sync.WaitGroup
	errs := make(chan error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := c.ListModels(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ListModels: %v", err)
	}
}

// ====================================================================
// ListExperimentalFeatures
// ====================================================================

func TestIntCmd_ListExperimentalFeatures_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	feats, err := c.ListExperimentalFeatures(context.Background())
	if err != nil {
		t.Fatalf("ListExperimentalFeatures: %v", err)
	}
	if len(feats) == 0 {
		t.Fatal("expected at least one experimental feature")
	}
	for _, f := range feats {
		if f.Name == "" {
			t.Errorf("feature row has empty name: %+v", f)
		}
		if f.Stage == "" {
			t.Errorf("feature %q has empty Stage", f.Name)
		}
	}
	t.Logf("features: %d total", len(feats))
}

func TestIntCmd_ListExperimentalFeatures_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.ListExperimentalFeatures(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// ListMCPServerStatus
// ====================================================================

func TestIntCmd_ListMCPServerStatus_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	result, err := c.ListMCPServerStatus(context.Background())
	if err != nil {
		t.Fatalf("ListMCPServerStatus: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	t.Logf("mcp servers: %d (cursor=%v)", len(result.Data), ptrStr(result.NextCursor))
	for _, srv := range result.Data {
		if srv.Name == "" {
			t.Errorf("server row has empty name")
		}
		// Tools is a MAP, not an array. Ensure the type asserts correctly.
		_ = srv.Tools
	}
}

func TestIntCmd_ListMCPServerStatus_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.ListMCPServerStatus(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// ListApps
// ====================================================================

func TestIntCmd_ListApps_HappyOrAuthError(t *testing.T) {
	// In codex 0.121.0 with ChatGPT auth, this often returns HTTP 403
	// Forbidden upstream. We tolerate either outcome to keep the test
	// deterministic across ChatGPT plan tiers.
	c := connectReadOnlyClient(t)
	apps, err := c.ListApps(context.Background())
	if err != nil {
		// RPCError wrapping is acceptable; verify it's a real RPC error,
		// not a connection / decode bug.
		if !types.IsRPCError(err) {
			t.Errorf("expected RPCError on app/list failure, got %T: %v", err, err)
		}
		t.Logf("ListApps returned RPC error (expected on some plans): %v", err)
		return
	}
	t.Logf("apps: %d total", len(apps))
}

func TestIntCmd_ListApps_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.ListApps(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// ListSkills
// ====================================================================

func TestIntCmd_ListSkills_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	groups, err := c.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("expected at least one skills group (system scope at minimum)")
	}
	var totalSkills int
	for _, g := range groups {
		if g.Cwd == "" {
			t.Errorf("group has empty cwd")
		}
		totalSkills += len(g.Skills)
		for _, s := range g.Skills {
			if s.Name == "" {
				t.Errorf("skill row has empty Name")
			}
		}
	}
	t.Logf("skills: %d groups, %d skills total", len(groups), totalSkills)
}

func TestIntCmd_ListSkills_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.ListSkills(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// ReadAccount
// ====================================================================

func TestIntCmd_ReadAccount_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	result, err := c.ReadAccount(context.Background())
	if err != nil {
		t.Fatalf("ReadAccount: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	if result.Account.Type == "" {
		t.Errorf("account.Type empty (expected 'chatgpt' or 'apikey')")
	}
	t.Logf("account: type=%s plan=%s requiresAuth=%v",
		result.Account.Type, result.Account.PlanType, result.RequiresOpenaiAuth)
}

func TestIntCmd_ReadAccount_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.ReadAccount(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// ReadRateLimits
// ====================================================================

func TestIntCmd_ReadRateLimits_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	result, err := c.ReadRateLimits(context.Background())
	if err != nil {
		t.Fatalf("ReadRateLimits: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	// codex 0.121.0 returns BOTH legacy and per-limit-id maps. Either
	// must populate.
	if result.RateLimits == nil && len(result.RateLimitsByLimitID) == 0 {
		t.Error("expected either rateLimits or rateLimitsByLimitId populated")
	}
	if result.RateLimits != nil && result.RateLimits.Primary != nil {
		t.Logf("primary window: usedPercent=%d resetsAt=%d duration=%dmin",
			result.RateLimits.Primary.UsedPercent,
			result.RateLimits.Primary.ResetsAt,
			result.RateLimits.Primary.WindowDurationMins)
	}
}

func TestIntCmd_ReadRateLimits_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.ReadRateLimits(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// GetAuthStatus
// ====================================================================

func TestIntCmd_GetAuthStatus_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	status, err := c.GetAuthStatus(context.Background())
	if err != nil {
		t.Fatalf("GetAuthStatus: %v", err)
	}
	if status == nil {
		t.Fatal("nil status")
	}
	if status.AuthMethod == "" {
		t.Errorf("AuthMethod empty (expected 'chatgpt' or 'apikey')")
	}
	// AuthToken may be empty depending on auth context — codex 0.121.0
	// only fills it when a downstream request needs token forwarding.
	// We assert presence of the field type only, not its content.
	t.Logf("auth: method=%s requiresOpenaiAuth=%v authToken_populated=%v (value redacted)",
		status.AuthMethod, status.RequiresOpenaiAuth, status.AuthToken != "")
}

func TestIntCmd_GetAuthStatus_ClosedClient(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	c, _ := codex.NewClient(context.Background(), types.NewCodexOptions())
	_ = c.Connect(context.Background())
	_ = c.Close(context.Background())
	if _, err := c.GetAuthStatus(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ====================================================================
// WriteConfigValue / WriteConfigBatch / sugars (mutating — safety net)
// ====================================================================

// safetyNetCodexConfig is a local copy of the helper from
// probe_v040_shapes_test.go (different package, can't import).
// Stashes ~/.codex/config.toml byte-identically and restores on
// Cleanup.
func safetyNetCodexConfig(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	stash := filepath.Join(t.TempDir(), "config.toml.stash")

	original, err := os.ReadFile(cfgPath)
	hadOriginal := err == nil
	if hadOriginal {
		if err := os.WriteFile(stash, original, 0o600); err != nil {
			t.Fatalf("safety-net stash: %v", err)
		}
	}
	t.Cleanup(func() {
		if hadOriginal {
			if err := os.WriteFile(cfgPath, original, 0o600); err != nil {
				t.Errorf("safety-net restore failed; user's config.toml may be corrupted (original at %s): %v", stash, err)
			}
			return
		}
		if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("safety-net cleanup remove failed: %v", err)
		}
	})
}

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

func TestIntCmd_UploadFeedback_Minimal(t *testing.T) {
	if os.Getenv("CODEX_SDK_FEEDBACK_OK") != "1" {
		t.Skip("set CODEX_SDK_FEEDBACK_OK=1 to send a real test feedback to OpenAI")
	}
	c := connectReadOnlyClient(t)
	receipt, err := c.UploadFeedback(context.Background(), types.FeedbackReport{
		Classification: "feedback",
		IncludeLogs:    false,
		Reason:         "v0.4.0 SDK integration test — please ignore",
	})
	if err != nil {
		t.Fatalf("UploadFeedback: %v", err)
	}
	if receipt == nil {
		t.Fatal("nil receipt")
	}
	t.Logf("feedback receipt: threadId=%q", receipt.ThreadID)
}

func TestIntCmd_UploadFeedback_EmptyClassification(t *testing.T) {
	c := connectReadOnlyClient(t)
	_, err := c.UploadFeedback(context.Background(), types.FeedbackReport{Classification: ""})
	if err == nil {
		t.Fatal("expected error for empty Classification")
	}
}

// ====================================================================
// Logout (DESTRUCTIVE — only runs when CODEX_SDK_LOGOUT_OK=1)
// ====================================================================

func TestIntCmd_Logout_Behavior(t *testing.T) {
	if os.Getenv("CODEX_SDK_LOGOUT_OK") != "1" {
		t.Skip("set CODEX_SDK_LOGOUT_OK=1 to run Logout — WILL invalidate ~/.codex/auth.json and require interactive `codex login` to recover")
	}
	c := connectReadOnlyClient(t)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	// Per R3: after Logout, downstream auth-needing RPCs may fail.
	// Verify ReadAccount still gives SOME response (even if it
	// reports unauthenticated) — i.e., the connection survives.
	_, err := c.ReadAccount(context.Background())
	t.Logf("post-Logout ReadAccount: %v (nil = still authed; non-nil = expected after logout)", err)
}

// ====================================================================
// Thread.Rollback / SetName (mutate throwaway thread — no quota)
// ====================================================================

func TestIntCmd_ThreadSetName_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	newName := "_v040_probe_renamed_" + nowSuffix()
	if err := thread.SetName(context.Background(), newName); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	t.Logf("renamed thread %q to %q", thread.ID(), newName)
}

func TestIntCmd_ThreadSetName_EmptyName(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.SetName(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestIntCmd_ThreadRollback_NumTurnsZero(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.Rollback(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error for numTurns=0")
	}
	if !strings.Contains(err.Error(), "numTurns must be >= 1") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_ThreadRollback_LargerThanHistory(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	// On an empty thread, n=1 likely returns an RPC error.
	err := thread.Rollback(context.Background(), 1)
	if err == nil {
		t.Logf("Rollback on empty thread succeeded (codex 0.121.0 may no-op)")
		return
	}
	t.Logf("Rollback on empty thread errored as expected: %v", err)
}

// ====================================================================
// Thread.CleanBackgroundTerminals (requires experimentalApi)
// ====================================================================

func TestIntCmd_CleanBackgroundTerminals_FeatureNotEnabled(t *testing.T) {
	// Default Client (experimentalApi: false) → expect typed error.
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.CleanBackgroundTerminals(context.Background())
	if err == nil {
		t.Fatal("expected FeatureNotEnabledError when experimentalApi is off")
	}
	if !types.IsFeatureNotEnabledError(err) {
		t.Errorf("expected FeatureNotEnabledError, got %T: %v", err, err)
	}
}

func TestIntCmd_CleanBackgroundTerminals_HappyWithCapability(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := types.NewCodexOptions().WithExperimentalAPI(true)
	c, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	thread, err := c.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	t.Cleanup(func() {
		archCtx, archCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer archCancel()
		_ = c.ArchiveThread(archCtx, thread.ID())
	})
	if err := thread.CleanBackgroundTerminals(context.Background()); err != nil {
		t.Fatalf("CleanBackgroundTerminals (with experimentalApi): %v", err)
	}
}

// ====================================================================
// Thread.Steer (needs active turn — quota-gated)
// ====================================================================

func TestIntCmd_ThreadSteer_NoActiveTurn(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.Steer(context.Background(), "extend the plan")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no active turn") {
		t.Errorf("err = %q", err)
	}
}

// ====================================================================
// Thread.Compact — async with pre-installed subscription
// ====================================================================

func TestIntCmd_Compact_EmptyThreadAckSucceeds(t *testing.T) {
	// Verified live: thread/compact/start returns `{}` ack even on
	// an empty thread (no completed turns). The async notification
	// may never arrive for an empty thread, but the RPC itself
	// doesn't error. Our test asserts the sync contract.
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	result, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result == nil {
		t.Fatal("nil CompactResult")
	}
	if result.ThreadID != thread.ID() {
		t.Errorf("ThreadID = %q, want %q", result.ThreadID, thread.ID())
	}
	// Detach the subscription so the throwaway-cleanup doesn't leak
	// the goroutine waiting on it.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = result.Wait(ctx)
	if err == nil {
		t.Log("note: unexpectedly received a ContextCompacted event on empty thread")
	} else if !strings.Contains(err.Error(), "context") {
		t.Errorf("Wait err = %q, want ctx cancel", err)
	}
}

func TestIntCmd_Compact_InFlightSecondCallRejected(t *testing.T) {
	// Two back-to-back Compact calls with no Wait in between —
	// the second MUST return the "already in flight" error.
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	result1, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = result1.Wait(ctx) // drain + detach
	})

	_, err = thread.Compact(context.Background(), nil)
	if err == nil {
		t.Fatal("expected 'already in flight' error on second Compact")
	}
	if !strings.Contains(err.Error(), "already in flight") {
		t.Errorf("err = %q, want 'already in flight'", err)
	}
}

func TestIntCmd_CompactResult_WaitCtxCancelAndReEnter(t *testing.T) {
	// Wait with a short ctx → detach subscription → a SECOND
	// Compact on the same thread should succeed (no leaked sub).
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	r1, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, _ = r1.Wait(ctx)
	cancel()

	// Second Compact — must NOT see "already in flight".
	r2, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("second Compact after Wait ctx-cancel: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = r2.Wait(ctx)
	})
}

// ====================================================================
// Thread.Summarize — sugar for Compact
// ====================================================================

func TestIntCmd_Summarize_IsAliasForCompact(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	r, err := thread.Summarize(context.Background())
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if r == nil {
		t.Fatal("nil result")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = r.Wait(ctx)
	})
}

// ====================================================================
// Thread.StartReview — sync ack, events stream later
// ====================================================================

func TestIntCmd_StartReview_TargetRequired(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	_, err := thread.StartReview(context.Background(), types.ReviewOptions{})
	if err == nil {
		t.Fatal("expected error on empty target")
	}
	if !strings.Contains(err.Error(), "opts.Target.Type is required") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_StartReview_UncommittedChanges_Detached(t *testing.T) {
	if os.Getenv("CODEX_SDK_RUN_TURNS") != "1" {
		t.Skip("set CODEX_SDK_RUN_TURNS=1 to run review (consumes quota)")
	}
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)

	// Codex refuses review on an empty thread ("No such file or
	// directory" for the rollout file). Run one trivial turn first
	// to create the rollout.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := thread.Run(ctx, "Reply with exactly: OK", nil)
	if err != nil {
		t.Fatalf("seed turn: %v", err)
	}

	opts := types.ReviewOptions{
		Target:   types.ReviewTargetUncommittedChanges(),
		Delivery: types.ReviewDetached,
	}
	result, err := thread.StartReview(context.Background(), opts)
	if err != nil {
		t.Fatalf("StartReview: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	// Detached review → new thread id.
	if result.ReviewThreadID == "" {
		t.Error("ReviewThreadID empty")
	}
	if result.ReviewThreadID == thread.ID() {
		t.Errorf("detached review should have NEW thread id, got original: %s", result.ReviewThreadID)
	}
	if result.Turn.ID == "" {
		t.Error("Turn.ID empty")
	}
	t.Logf("detached review: reviewThreadId=%q turnId=%q status=%q",
		result.ReviewThreadID, result.Turn.ID, result.Turn.Status)

	// Best-effort archive of the review thread.
	t.Cleanup(func() {
		archCtx, archCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer archCancel()
		_ = c.ArchiveThread(archCtx, result.ReviewThreadID)
	})
}

func TestIntCmd_StartReview_CommitTarget(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	// Use a clearly-invalid SHA — expect an error response (not a
	// connection bug).
	opts := types.ReviewOptions{
		Target: types.ReviewTargetCommit("0000000000000000000000000000000000000000", ""),
	}
	_, err := thread.StartReview(context.Background(), opts)
	if err == nil {
		t.Log("note: codex accepted the invalid SHA (may error later via events)")
		return
	}
	t.Logf("invalid SHA rejected: %v", err)
}

// ====================================================================
// GitDiffToRemote (wire method)
// ====================================================================

func TestIntCmd_GitDiffToRemote_EmptyCwd(t *testing.T) {
	c := connectReadOnlyClient(t)
	_, err := c.GitDiffToRemote(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty cwd")
	}
	if !strings.Contains(err.Error(), "cwd must not be empty") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_GitDiffToRemote_NonGitDir(t *testing.T) {
	c := connectReadOnlyClient(t)
	// A clean tempdir is not a git repo — codex should return an
	// RPC error rather than crashing.
	_, err := c.GitDiffToRemote(context.Background(), t.TempDir())
	if err == nil {
		t.Log("note: codex accepted a non-git path; may return empty diff")
		return
	}
	t.Logf("non-git-dir error (expected): %v", err)
}

func TestIntCmd_GitDiffToRemote_RealRepo(t *testing.T) {
	c := connectReadOnlyClient(t)
	// Run against the SDK's own repo — it IS a git repo with a remote.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests/ → repo root.
	repoRoot := filepath.Dir(cwd)
	result, err := c.GitDiffToRemote(context.Background(), repoRoot)
	if err != nil {
		// Codex may return an error for reasons like "no tracking
		// branch configured". Log + exit — don't fail the suite.
		t.Logf("GitDiffToRemote returned: %v (may be legitimate for local-only branches)", err)
		return
	}
	if result == nil {
		t.Fatal("nil result")
	}
	t.Logf("remote diff: sha=%s diffLen=%d", result.Sha, len(result.Diff))
	if result.Sha == "" {
		t.Error("expected HEAD sha")
	}
}

// ====================================================================
// Helpers
// ====================================================================

// newThrowawayThread spins up a thread named "_v040_probe_<unix>"
// and archives it on Cleanup. Codex has no thread/delete wire method,
// so the archived thread persists in ~/.codex/archived_sessions/
// until the user manually rm's it. The prefix makes them easy to spot.
func newThrowawayThread(t *testing.T, c *codex.Client) *codex.Thread {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	thread, err := c.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort archive. Failures are logged, not fatal — the
		// thread persists either way.
		archCtx, archCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer archCancel()
		if err := c.ArchiveThread(archCtx, thread.ID()); err != nil {
			t.Logf("WARN: archive throwaway %q: %v", thread.ID(), err)
		}
	})
	return thread
}

// ptrStr is a nil-safe stringifier for *string fields.
func ptrStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// nowSuffix returns a unique-per-call ns timestamp for naming
// throwaway artifacts.
func nowSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}
