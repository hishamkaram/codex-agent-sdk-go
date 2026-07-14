package codex

import (
	"context"
	"sync"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestCLICompatibilityArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    transport.SemVer
		known      bool
		callback   bool
		wantGlobal []string
		wantExtra  []string
	}{
		{
			name:      "unknown_peer_uses_legacy_hook_alias",
			callback:  true,
			wantExtra: []string{"--enable", "codex_hooks"},
		},
		{
			name:      "old_peer_uses_legacy_hook_alias_without_trust_flag",
			version:   transport.SemVer{Major: 0, Minor: 130, Patch: 0},
			known:     true,
			callback:  true,
			wantExtra: []string{"--enable", "codex_hooks"},
		},
		{
			name:       "trust_flag_first_supported_peer",
			version:    transport.SemVer{Major: 0, Minor: 131, Patch: 0},
			known:      true,
			callback:   true,
			wantGlobal: []string{"--dangerously-bypass-hook-trust"},
			wantExtra:  []string{"--enable", "codex_hooks"},
		},
		{
			name:       "recommended_peer_uses_canonical_hook_feature",
			version:    transport.SemVer{Major: 0, Minor: 144, Patch: 1},
			known:      true,
			callback:   true,
			wantGlobal: []string{"--dangerously-bypass-hook-trust"},
			wantExtra:  []string{"--enable", "hooks"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := types.NewCodexOptions().WithHooks(true)
			if tt.callback {
				opts.HookCallback = func(_ context.Context, _ types.HookInput) types.HookDecision { return types.HookAllow{} }
			}
			global, extra := cliCompatibilityArgs(opts, tt.version, tt.known)
			assertStringsEqual(t, "global args", global, tt.wantGlobal)
			assertStringsEqual(t, "extra args", extra, tt.wantExtra)
		})
	}
}

func TestValidateApprovalPolicyCompatibility(t *testing.T) {
	t.Parallel()

	removedAt := transport.SemVer{Major: 0, Minor: 143, Patch: 0}
	if err := validateApprovalPolicyCompatibility(legacyApprovalOnFailure, removedAt, true); err == nil {
		t.Fatal("on-failure must be rejected for a peer whose schema removed it")
	} else if !types.IsUnsupportedApprovalPolicyError(err) {
		t.Fatalf("error = %T, want UnsupportedApprovalPolicyError", err)
	}
	if err := validateApprovalPolicyCompatibility(
		legacyApprovalOnFailure,
		transport.SemVer{Major: 0, Minor: 142, Patch: 0},
		true,
	); err != nil {
		t.Fatalf("older peer unexpectedly rejected on-failure: %v", err)
	}
	if err := validateApprovalPolicyCompatibility(legacyApprovalOnFailure, transport.SemVer{}, false); err != nil {
		t.Fatalf("unprobed peer unexpectedly rejected on-failure: %v", err)
	}
}

func TestValidateApprovalPolicyConcurrentPublication(t *testing.T) {
	t.Parallel()

	c := &Client{}
	current := &cliCompatibilityState{
		version: transport.SemVer{Major: 0, Minor: 144, Patch: 1},
		known:   true,
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 1000 {
				c.cliCompatibility.Store(current)
			}
		}()
		go func() {
			defer wg.Done()
			for range 1000 {
				_ = c.validateApprovalPolicy(legacyApprovalOnFailure)
			}
		}()
	}
	wg.Wait()
}

func assertStringsEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %#v, want %#v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %#v, want %#v", label, got, want)
		}
	}
}
