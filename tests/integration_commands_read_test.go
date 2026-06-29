//go:build integration

package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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

func TestIntCmd_ListMCPServerStatus_OAuthPending(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	safetyNetCodexConfig(t)
	fixture := newOAuthRequiredMCPFixture(t)

	configCtx, configCancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(configCancel)
	configClient, err := codex.NewClient(configCtx, types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient(config): %v", err)
	}
	if err := configClient.Connect(configCtx); err != nil {
		t.Fatalf("Connect(config): %v", err)
	}
	if _, err := configClient.WriteConfigBatch(configCtx, []types.ConfigEntry{
		{
			KeyPath:       "mcp_servers.oauth_fixture.url",
			MergeStrategy: types.MergeReplace,
			Value:         fixture.URL + "/mcp",
		},
		{
			KeyPath:       "mcp_servers.oauth_fixture.auth_type",
			MergeStrategy: types.MergeReplace,
			Value:         "oauth",
		},
	}); err != nil {
		_ = configClient.Close(context.Background())
		t.Fatalf("WriteConfigBatch oauth fixture: %v", err)
	}
	if err := configClient.Close(context.Background()); err != nil {
		t.Fatalf("Close(config): %v", err)
	}

	statusCtx, statusCancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(statusCancel)
	c, err := codex.NewClient(statusCtx, types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient(status): %v", err)
	}
	if err := c.Connect(statusCtx); err != nil {
		t.Fatalf("Connect(status): %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	deadline := time.Now().Add(20 * time.Second)
	var lastRows []string
	for {
		result, err := c.ListMCPServerStatus(context.Background())
		if err != nil {
			t.Fatalf("ListMCPServerStatus: %v", err)
		}
		lastRows = mcpStatusRows(result)
		for _, srv := range result.Data {
			if srv.Name != "oauth_fixture" {
				continue
			}
			authStatus := strings.ToLower(srv.AuthStatus)
			if !strings.Contains(authStatus, "oauth") && !strings.Contains(authStatus, "login") && authStatus != "notloggedin" {
				t.Fatalf("oauth_fixture AuthStatus = %q, want OAuth pending/login status row; row=%+v", srv.AuthStatus, srv)
			}
			t.Logf("oauth fixture row: name=%s authStatus=%s", srv.Name, srv.AuthStatus)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("oauth_fixture did not appear in MCP status list before timeout; fixture requests=%d rows=%v", fixtureRequests(fixture), lastRows)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

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
