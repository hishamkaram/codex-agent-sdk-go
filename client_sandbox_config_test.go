package codex

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestSetSandboxWritesCanonicalConfigKey(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"status":"ok","version":"sha256:fixture","filePath":"/tmp/config.toml"}`)}
	})

	if err := c.SetSandbox(t.Context(), types.SandboxReadOnly); err != nil {
		t.Fatal(err)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "config/value/write")
	assertThreadConfigJSON(t, wire[0].Params, `{"keyPath":"sandbox_mode","value":"read-only","mergeStrategy":"replace"}`)
}

func TestReadConfigPreservesCanonicalSandboxAndRaw(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		config  string
		want    string
		wantRaw string
	}{
		{"canonical", `{"sandbox_mode":"read-only","sandbox":"workspace-write","extra":{"flag":true}}`, "read-only", `"read-only"`},
		{"null", `{"sandbox_mode":null,"sandbox":"workspace-write","extra":{"flag":true}}`, "", "null"},
		{"omitted", `{"sandbox":"workspace-write","extra":{"flag":true}}`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"config":` + tc.config + `}`)}
			})
			config, err := c.ReadConfig(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if config.Sandbox != nil {
					t.Fatalf("sandbox = %q, want unset", *config.Sandbox)
				}
			} else if config.Sandbox == nil || *config.Sandbox != tc.want {
				t.Fatalf("sandbox = %v, want %q", config.Sandbox, tc.want)
			}
			if got := string(config.Raw["sandbox_mode"]); got != tc.wantRaw {
				t.Errorf("raw sandbox_mode = %q, want %q", got, tc.wantRaw)
			}
			if string(config.Raw["sandbox"]) != `"workspace-write"` || string(config.Raw["extra"]) != `{"flag":true}` {
				t.Errorf("raw config lost fields: %v", config.Raw)
			}
			assertThreadRequestMethods(t, observedThreadRequests(t, server), "config/read")
		})
	}
}

func TestSetSandboxReportsOverrideWithoutRetry(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"status":"okOverridden","version":"sha256:fixture","filePath":"/tmp/config.toml"}`)}
	})
	err := c.SetSandbox(t.Context(), types.SandboxReadOnly)
	if !errors.Is(err, ErrSandboxConfigOverridden) || !strings.Contains(err.Error(), "may have been saved") {
		t.Fatalf("SetSandbox error = %v, want override with possible persistence", err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "config/value/write")

	write, err := c.WriteConfigValue(t.Context(), "sandbox_mode", types.SandboxReadOnly, types.MergeReplace)
	if err != nil || write == nil || write.Status != "okOverridden" {
		t.Fatalf("WriteConfigValue = %+v, %v, want provider status", write, err)
	}
}

func TestSetSandboxRequiresWriteStatus(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"version":"sha256:fixture","filePath":"/tmp/config.toml"}`)}
	})
	if err := c.SetSandbox(t.Context(), types.SandboxReadOnly); err == nil {
		t.Fatal("SetSandbox succeeded without a write status")
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "config/value/write")
}
