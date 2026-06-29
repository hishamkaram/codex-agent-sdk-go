//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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

func mcpStatusRows(result *types.MCPServerStatusListResult) []string {
	if result == nil {
		return nil
	}
	rows := make([]string, 0, len(result.Data))
	for _, srv := range result.Data {
		rows = append(rows, srv.Name+":"+srv.AuthStatus)
	}
	return rows
}

// ====================================================================
// ListApps
// ====================================================================

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

type oauthMCPFixture struct {
	*httptest.Server
	requests atomic.Int32
}

func newOAuthRequiredMCPFixture(t *testing.T) *oauthMCPFixture {
	t.Helper()

	fixture := &oauthMCPFixture{}
	var serverURL string
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requests.Add(1)

		switch {
		case r.URL.Path == "/mcp":
			metadataURL := serverURL + "/.well-known/oauth-protected-resource"
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, metadataURL))
			http.Error(w, "authorization required", http.StatusUnauthorized)
		case r.URL.Path == "/.well-known/oauth-protected-resource":
			writeJSON(w, map[string]any{
				"resource":              serverURL + "/mcp",
				"authorization_servers": []string{serverURL},
			})
		case r.URL.Path == "/.well-known/oauth-authorization-server":
			writeJSON(w, map[string]any{
				"issuer":                                serverURL,
				"authorization_endpoint":                serverURL + "/authorize",
				"token_endpoint":                        serverURL + "/token",
				"registration_endpoint":                 serverURL + "/register",
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
				"code_challenge_methods_supported":      []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case r.URL.Path == "/register":
			writeJSONWithStatus(w, http.StatusCreated, map[string]any{
				"client_id":                  "agentd-sdk-test-client",
				"redirect_uris":              []string{"http://127.0.0.1/callback"},
				"token_endpoint_auth_method": "none",
			})
		case r.URL.Path == "/authorize":
			http.Error(w, "interactive OAuth required", http.StatusBadRequest)
		case r.URL.Path == "/token":
			http.Error(w, "token unavailable in test fixture", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = fixture.URL
	t.Cleanup(fixture.Close)
	return fixture
}

func fixtureRequests(f *oauthMCPFixture) int32 {
	if f == nil {
		return 0
	}
	return f.requests.Load()
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONWithStatus(w, http.StatusOK, v)
}

func writeJSONWithStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
