//go:build integration

// Probe test for v0.4.0: discovers wire shapes by sending each unverified
// RPC against a live codex 0.121.0 and dumping raw responses to
// tests/fixtures/v040_probes/<method>.json.
//
// Lives in the root `codex` package so it can call c.demux.Send directly
// (the public Client API does not expose arbitrary RPCs by design — see
// Risk R4 in the v0.4.0 plan).
//
// Gated on CODEX_SDK_PROBE=1 so it never runs in normal CI. After Batch
// 1 lands, this file is deleted; fixtures stay as regression baselines.
//
// Safety nets:
//   - safetyNetCodexConfig stashes ~/.codex/config.toml byte-identically
//     and restores on Cleanup, regardless of test crashes.
//   - Throwaway threads use prefix "_v040_probe_<unix>" and are archived
//     on Cleanup. The test prints a WARNING listing archived thread paths
//     so the maintainer can manually rm if desired (codex has no
//     thread/delete wire method).
//   - account/logout, feedback/upload, turn/steer, review/start are
//     SKIPPED — too dangerous (auth wipe), too costly (quota), or
//     deferred to Batch 3/4.
//
// Privacy: redactSensitive walks the JSON response and masks
// PII-shaped field values (email, loginId, *token*, *secret*, etc.)
// before writing to the fixture file. Raw payload is logged via t.Logf
// (logs are not committed).
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// probeFixtureDir is where every probe writes its redacted fixture.
const probeFixtureDir = "tests/fixtures/v040_probes"

// throwawayThreadPrefix marks throwaway threads created by the probe so
// the maintainer can identify and clean them later.
const throwawayThreadPrefix = "_v040_probe_"

// requireProbeGate skips the test unless explicitly opted in.
func requireProbeGate(t *testing.T) {
	t.Helper()
	if os.Getenv("CODEX_SDK_PROBE") != "1" {
		t.Skip("set CODEX_SDK_PROBE=1 to run v0.4.0 wire-shape probes (mutates ~/.codex/config.toml — protected by safety net)")
	}
}

// requireCodexCLI is a local copy of the helper from tests/integration_test.go
// (different package, can't import).
func requireCodexCLI(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/null"); err != nil {
		// just sanity
		t.Fatal(err)
	}
	// Defer the actual codex resolution to Connect — Connect fails with a
	// clear error if codex isn't on PATH.
}

// requireAuthCfg checks for OPENAI_API_KEY or ~/.codex/auth.json.
func requireAuthCfg(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENAI_API_KEY") != "" {
		return
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".codex", "auth.json")); err == nil {
		return
	}
	t.Skip("no OPENAI_API_KEY and no ~/.codex/auth.json — cannot auth")
}

// safetyNetCodexConfig stashes ~/.codex/config.toml to a test-local file
// and registers an unconditional Cleanup that restores (or removes if
// none existed). Mirrors safetyNetHooksJSON pattern from v0.3.0.
func safetyNetCodexConfig(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	stash := filepath.Join(t.TempDir(), "config.toml.stash")

	original, hadOriginal, err := readIfExists(cfgPath)
	if err != nil {
		t.Fatalf("safety-net read: %v", err)
	}
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

// piiFieldNames is the set of JSON field names whose VALUES get redacted
// in the committed fixture. Match is case-insensitive substring.
var piiFieldNames = []string{
	"email", "loginid", "userid", "accountid",
	"apikey", "token", "password", "secret",
}

// redactSensitive walks the JSON tree and replaces values of PII-shaped
// fields with "<REDACTED>". Returns formatted JSON suitable for the
// fixture file.
func redactSensitive(raw json.RawMessage) ([]byte, error) {
	var any interface{}
	if err := json.Unmarshal(raw, &any); err != nil {
		// Not JSON-decodable — return raw as-is.
		return raw, nil
	}
	redacted := redactWalk(any)
	return json.MarshalIndent(redacted, "", "  ")
}

func redactWalk(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, vv := range t {
			if isSensitiveField(k) {
				out[k] = "<REDACTED>"
			} else {
				out[k] = redactWalk(vv)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, vv := range t {
			out[i] = redactWalk(vv)
		}
		return out
	default:
		return v
	}
}

func isSensitiveField(name string) bool {
	lower := strings.ToLower(name)
	for _, pii := range piiFieldNames {
		if strings.Contains(lower, pii) {
			return true
		}
	}
	return false
}

// dumpFixture writes the redacted JSON to tests/fixtures/v040_probes/<methodSafe>.json
// and logs the raw response (uncommitted) for the maintainer.
func dumpFixture(t *testing.T, method string, raw json.RawMessage) {
	t.Helper()
	t.Logf("RAW response for %s: %s", method, string(raw))

	if err := os.MkdirAll(probeFixtureDir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	safeName := strings.ReplaceAll(method, "/", "_") + ".json"
	out := filepath.Join(probeFixtureDir, safeName)

	redacted, err := redactSensitive(raw)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if err := os.WriteFile(out, redacted, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("wrote redacted fixture: %s (%d bytes)", out, len(redacted))
}

// dumpErrorFixture writes the error shape (when the RPC returns an error
// rather than a result) so we can derive ErrFeatureNotEnabled etc.
func dumpErrorFixture(t *testing.T, method, suffix string, rpcErr *jsonrpc.RPCError) {
	t.Helper()
	if err := os.MkdirAll(probeFixtureDir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	safeName := strings.ReplaceAll(method, "/", "_") + "_error_" + suffix + ".json"
	out := filepath.Join(probeFixtureDir, safeName)
	body, _ := json.MarshalIndent(rpcErr, "", "  ")
	if err := os.WriteFile(out, body, 0o644); err != nil {
		t.Fatalf("write error fixture: %v", err)
	}
	t.Logf("wrote error fixture: %s (code=%d msg=%q)", out, rpcErr.Code, rpcErr.Message)
}

// connectProbeClient spins up a live codex Client. Caller's t.Cleanup
// closes it.
func connectProbeClient(t *testing.T) *Client {
	t.Helper()
	requireCodexCLI(t)
	requireAuthCfg(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	opts := types.NewCodexOptions()
	c, err := NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// throwawayThread creates a thread named "_v040_probe_<unix>" and
// archives it on Cleanup. Returns the Thread.
func throwawayThread(t *testing.T, c *Client) *Thread {
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
		archCtx, archCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer archCancel()
		if _, err := c.demux.Send(archCtx, "thread/archive", map[string]any{"threadId": thread.ID()}); err != nil {
			t.Logf("WARN: failed to archive throwaway thread %q: %v", thread.ID(), err)
		} else {
			t.Logf("archived throwaway thread %q (still in ~/.codex/archived_sessions/ — codex has no delete API)",
				thread.ID())
		}
	})
	return thread
}

// probeRPC sends a method+params and dumps either the result fixture or
// the error fixture. Returns the raw result for the caller to inspect
// (e.g., to extract an ID for the next probe).
func probeRPC(t *testing.T, c *Client, method string, params any) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.demux.Send(ctx, method, params)
	if err != nil {
		t.Fatalf("%s: transport error: %v", method, err)
	}
	if resp.Error != nil {
		dumpErrorFixture(t, method, "rpc", resp.Error)
		return nil
	}
	dumpFixture(t, method, resp.Result)
	return resp.Result
}

// ====================================================================
// Probes — one subtest per method
// ====================================================================

func TestProbeV040Shapes(t *testing.T) {
	requireProbeGate(t)
	safetyNetCodexConfig(t)
	c := connectProbeClient(t)

	// --- Read-only probes (no state mutation) -----------------------

	t.Run("model_list", func(t *testing.T) {
		probeRPC(t, c, "model/list", map[string]any{})
	})

	t.Run("experimentalFeature_list", func(t *testing.T) {
		probeRPC(t, c, "experimentalFeature/list", map[string]any{})
	})

	t.Run("mcpServerStatus_list", func(t *testing.T) {
		probeRPC(t, c, "mcpServerStatus/list", map[string]any{})
	})

	t.Run("app_list", func(t *testing.T) {
		probeRPC(t, c, "app/list", map[string]any{})
	})

	t.Run("skills_list", func(t *testing.T) {
		probeRPC(t, c, "skills/list", map[string]any{})
	})

	t.Run("account_read", func(t *testing.T) {
		probeRPC(t, c, "account/read", map[string]any{})
	})

	t.Run("account_rateLimits_read", func(t *testing.T) {
		probeRPC(t, c, "account/rateLimits/read", map[string]any{})
	})

	t.Run("config_read", func(t *testing.T) {
		probeRPC(t, c, "config/read", map[string]any{})
	})

	t.Run("getAuthStatus", func(t *testing.T) {
		probeRPC(t, c, "getAuthStatus", map[string]any{})
	})

	// --- Mutating probes (config writes — protected by safety net) --

	t.Run("config_value_write_known_key", func(t *testing.T) {
		// Read current model first, then write it back (semantic no-op).
		readResp, err := c.demux.Send(context.Background(), "config/read", map[string]any{})
		if err != nil || readResp.Error != nil {
			t.Skipf("could not read current config to round-trip: err=%v rpcErr=%v", err, readResp.Error)
		}
		var cfgMap map[string]json.RawMessage
		if err := json.Unmarshal(readResp.Result, &cfgMap); err != nil {
			t.Skipf("could not unmarshal config: %v", err)
		}
		currentModel := cfgMap["model"]
		if currentModel == nil {
			currentModel = json.RawMessage(`"gpt-5.4"`) // fallback
		}
		probeRPC(t, c, "config/value/write", map[string]any{
			"path":  "model",
			"value": json.RawMessage(currentModel),
		})
	})

	t.Run("config_value_write_unknown_key", func(t *testing.T) {
		// Capture the error shape for unknown-key writes — useful for
		// ErrFeatureNotEnabled-style typed errors.
		probeRPC(t, c, "config/value/write", map[string]any{
			"path":  "_v040_probe_marker_does_not_exist",
			"value": "x",
		})
	})

	t.Run("config_batchWrite", func(t *testing.T) {
		// Round-trip: read current, batch-write same.
		readResp, err := c.demux.Send(context.Background(), "config/read", map[string]any{})
		if err != nil || readResp.Error != nil {
			t.Skipf("could not read current config: err=%v rpcErr=%v", err, readResp.Error)
		}
		var cfgMap map[string]json.RawMessage
		_ = json.Unmarshal(readResp.Result, &cfgMap)
		currentModel := cfgMap["model"]
		if currentModel == nil {
			currentModel = json.RawMessage(`"gpt-5.4"`)
		}
		// Try a few common batch param shapes — the binary strings hint at
		// "entries" but didn't confirm. Fall back to "values".
		probeRPC(t, c, "config/batchWrite", map[string]any{
			"entries": []map[string]any{
				{"path": "model", "value": json.RawMessage(currentModel)},
			},
		})
	})

	// --- Throwaway-thread probes ------------------------------------

	t.Run("thread_name_set", func(t *testing.T) {
		thread := throwawayThread(t, c)
		newName := fmt.Sprintf("%s%d", throwawayThreadPrefix, time.Now().UnixNano())
		probeRPC(t, c, "thread/name/set", map[string]any{
			"threadId": thread.ID(),
			"name":     newName,
		})
	})

	t.Run("thread_compact_start_empty", func(t *testing.T) {
		thread := throwawayThread(t, c)
		// Compact on a brand-new (empty) thread — captures the ack shape
		// even if the server no-ops.
		probeRPC(t, c, "thread/compact/start", map[string]any{
			"threadId": thread.ID(),
		})
	})

	t.Run("thread_rollback_zero", func(t *testing.T) {
		thread := throwawayThread(t, c)
		// n=0 likely errors — captures the param-validation error shape.
		probeRPC(t, c, "thread/rollback", map[string]any{
			"threadId": thread.ID(),
			"n":        0,
		})
	})

	t.Run("thread_rollback_one", func(t *testing.T) {
		thread := throwawayThread(t, c)
		// n=1 on empty thread also likely errors (history too short) but
		// captures a different error path than n=0.
		probeRPC(t, c, "thread/rollback", map[string]any{
			"threadId": thread.ID(),
			"n":        1,
		})
	})

	t.Run("backgroundTerminals_clean", func(t *testing.T) {
		thread := throwawayThread(t, c)
		// Even with no terminals running, the method should succeed and
		// give us its response shape.
		probeRPC(t, c, "thread/backgroundTerminals/clean", map[string]any{
			"threadId": thread.ID(),
		})
	})

	t.Run("gitDiffToRemote", func(t *testing.T) {
		// Probe whether this works without a thread / what params it takes.
		probeRPC(t, c, "gitDiffToRemote", map[string]any{})
	})
}
