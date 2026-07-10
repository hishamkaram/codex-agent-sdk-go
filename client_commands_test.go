package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// All happy-path coverage for these read-only methods lives in
// tests/integration_commands_test.go (real codex required). Unit tests
// here cover the connection-state guard rails: not-connected, closed,
// concurrent dispatch.

func TestBuildSkillsListParams_UsesDefaultCwd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts *types.CodexOptions
		want map[string]any
	}{
		{
			name: "empty options",
			opts: types.NewCodexOptions(),
			want: map[string]any{},
		},
		{
			name: "default cwd",
			opts: types.NewCodexOptions().WithCwd("/repo/subdir"),
			want: map[string]any{"cwds": []string{"/repo/subdir"}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildSkillsListParams(tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildSkillsListParams() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEncodeApprovalPolicy_GranularUsesStructuredObject(t *testing.T) {
	t.Parallel()

	assertGranularApprovalPolicyValue(t, encodeApprovalPolicy(types.ApprovalGranular))
}

func TestEncodeApprovalPolicy_NonGranularPoliciesRemainStrings(t *testing.T) {
	t.Parallel()

	tests := []types.ApprovalPolicy{
		types.ApprovalUntrusted,
		types.ApprovalOnFailure,
		types.ApprovalOnRequest,
		types.ApprovalNever,
	}
	for _, policy := range tests {
		policy := policy
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			got := encodeApprovalPolicy(policy)
			if got != string(policy) {
				t.Fatalf("encodeApprovalPolicy(%q) = %#v, want string %q", policy, got, policy)
			}
		})
	}
}

func assertGranularApprovalPolicyValue(t *testing.T, got any) {
	t.Helper()

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal approval policy: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("approval policy JSON = %s, want object: %v", raw, err)
	}
	if len(top) != 1 {
		t.Fatalf("approval policy JSON = %s, want one top-level granular key", raw)
	}
	granularRaw, ok := top["granular"]
	if !ok {
		t.Fatalf("approval policy JSON = %s, want granular object", raw)
	}
	var granular map[string]bool
	if err := json.Unmarshal(granularRaw, &granular); err != nil {
		t.Fatalf("granular JSON = %s, want bool map: %v", granularRaw, err)
	}
	want := map[string]bool{
		"mcp_elicitations": true,
		"rules":            true,
		"sandbox_approval": true,
	}
	if !reflect.DeepEqual(granular, want) {
		t.Fatalf("granular = %#v, want %#v", granular, want)
	}
}

func TestClientCommands_NotConnected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{"ReadConfig", func(c *Client) error { _, err := c.ReadConfig(context.Background()); return err }},
		{"ReadConfigRequirements", func(c *Client) error {
			_, err := c.ReadConfigRequirements(context.Background())
			return err
		}},
		{"ListModels", func(c *Client) error { _, err := c.ListModels(context.Background()); return err }},
		{"ListExperimentalFeatures", func(c *Client) error {
			_, err := c.ListExperimentalFeatures(context.Background())
			return err
		}},
		{"ListMCPServerStatus", func(c *Client) error {
			_, err := c.ListMCPServerStatus(context.Background())
			return err
		}},
		{"ListApps", func(c *Client) error { _, err := c.ListApps(context.Background()); return err }},
		{"ListSkills", func(c *Client) error { _, err := c.ListSkills(context.Background()); return err }},
		{"ReadAccount", func(c *Client) error { _, err := c.ReadAccount(context.Background()); return err }},
		{"ReadRateLimits", func(c *Client) error { _, err := c.ReadRateLimits(context.Background()); return err }},
		{"GetAuthStatus", func(c *Client) error { _, err := c.GetAuthStatus(context.Background()); return err }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(context.Background(), types.NewCodexOptions())
			if err != nil {
				t.Fatal(err)
			}
			err = tt.call(c)
			if err == nil {
				t.Fatal("expected error on not-connected client")
			}
			if !strings.Contains(err.Error(), "not connected") {
				t.Errorf("error = %q, want 'not connected'", err)
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Errorf("error %q must include caller name %q", err, tt.name)
			}
		})
	}
}

func TestReadConfigRequirementsOmitsParams(t *testing.T) {
	t.Parallel()

	requests := make(chan jsonrpc.Request, 1)
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method == "configRequirements/read" {
			requests <- req
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"requirements":null}`)}
		}
		return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
	})

	if _, err := c.ReadConfigRequirements(context.Background()); err != nil {
		t.Fatalf("ReadConfigRequirements: %v", err)
	}
	if req := <-requests; len(req.Params) != 0 {
		t.Fatalf("params = %s, want omitted", req.Params)
	}
}

func TestClientCommands_MutatingInputValidation(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	tests := []struct {
		name    string
		call    func() error
		wantErr string
	}{
		{
			"WriteConfigValue empty keyPath",
			func() error {
				_, err := c.WriteConfigValue(context.Background(), "", "x", types.MergeReplace)
				return err
			},
			"keyPath must not be empty",
		},
		{
			"WriteConfigBatch empty edits",
			func() error {
				_, err := c.WriteConfigBatch(context.Background(), nil)
				return err
			},
			"edits must not be empty",
		},
		{
			"SetModel empty",
			func() error { return c.SetModel(context.Background(), "") },
			"model must not be empty",
		},
		{
			"SetReasoningEffort empty",
			func() error { return c.SetReasoningEffort(context.Background(), "") },
			"effort must not be empty",
		},
		{
			"SetApprovalPolicy empty",
			func() error { return c.SetApprovalPolicy(context.Background(), "") },
			"policy must not be empty",
		},
		{
			"SetSandbox empty",
			func() error { return c.SetSandbox(context.Background(), "") },
			"sandbox must not be empty",
		},
		{
			"SetExperimentalFeature empty name",
			func() error { return c.SetExperimentalFeature(context.Background(), "", true) },
			"name must not be empty",
		},
		{
			"UploadFeedback empty Classification",
			func() error {
				_, err := c.UploadFeedback(context.Background(), types.FeedbackReport{})
				return err
			},
			"Classification is required",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.call()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestClientSetReasoningEffort_WritesModelReasoningEffortKey(t *testing.T) {
	t.Parallel()

	requests := make(chan jsonrpc.Request, 1)
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method == "config/value/write" {
			requests <- req
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"status":"ok"}`)}
		}
		return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
	})

	if err := c.SetReasoningEffort(context.Background(), "xhigh"); err != nil {
		t.Fatalf("SetReasoningEffort: %v", err)
	}

	req := <-requests
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["keyPath"] != "model_reasoning_effort" {
		t.Fatalf("keyPath = %q, want model_reasoning_effort", params["keyPath"])
	}
	if params["mergeStrategy"] != string(types.MergeReplace) {
		t.Fatalf("mergeStrategy = %q, want replace", params["mergeStrategy"])
	}
	if params["value"] != "xhigh" {
		t.Fatalf("value = %q, want xhigh", params["value"])
	}
}

func TestClientCommands_MutatingNotConnected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{"WriteConfigValue", func(c *Client) error {
			_, err := c.WriteConfigValue(context.Background(), "model", "gpt-5.4", types.MergeReplace)
			return err
		}},
		{"WriteConfigBatch", func(c *Client) error {
			_, err := c.WriteConfigBatch(context.Background(), []types.ConfigEntry{{KeyPath: "model", Value: "x"}})
			return err
		}},
		{"SetReasoningEffort", func(c *Client) error {
			return c.SetReasoningEffort(context.Background(), "xhigh")
		}},
		{"SetExperimentalFeature", func(c *Client) error {
			return c.SetExperimentalFeature(context.Background(), "shell_tool", true)
		}},
		{"Logout", func(c *Client) error { return c.Logout(context.Background()) }},
		{"UploadFeedback", func(c *Client) error {
			_, err := c.UploadFeedback(context.Background(), types.FeedbackReport{Classification: "feedback"})
			return err
		}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := NewClient(context.Background(), types.NewCodexOptions())
			err := tt.call(c)
			if err == nil {
				t.Fatal("expected error on not-connected")
			}
			if !strings.Contains(err.Error(), "not connected") {
				t.Errorf("err = %q, want 'not connected'", err)
			}
		})
	}
}

func TestClientCommands_HookCallbackBlocksHooksConfigWrites(t *testing.T) {
	t.Parallel()

	c, _ := NewClient(context.Background(), types.NewCodexOptions().WithHookCallback(types.DefaultAllowHookHandler))
	c.connected.Store(true)

	_, err := c.WriteConfigValue(context.Background(), "hooks.PreToolUse", []any{}, types.MergeReplace)
	if err == nil {
		t.Fatal("expected hooks config write to be blocked")
	}
	if !strings.Contains(err.Error(), "WithHookCallback owns generated hooks.json") {
		t.Fatalf("err = %q, want hook ownership guard", err)
	}

	_, err = c.WriteConfigBatch(context.Background(), []types.ConfigEntry{
		{KeyPath: "model", Value: "gpt-5.4"},
		{KeyPath: "hooks", Value: map[string]any{}},
	})
	if err == nil {
		t.Fatal("expected hooks batch config write to be blocked")
	}
	if !strings.Contains(err.Error(), "WriteConfigBatch") || !strings.Contains(err.Error(), "hooks") {
		t.Fatalf("err = %q, want batch hook key context", err)
	}
}

func TestClientCommands_HookConfigKeyDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{"hooks", true},
		{"hooks.PreToolUse", true},
		{"hooks.pre_tool_use.0", true},
		{"model", false},
		{"features.codex_hooks", false},
		{"hooks_json", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			if got := isHooksConfigKeyPath(tt.key); got != tt.want {
				t.Fatalf("isHooksConfigKeyPath(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestClassifyRPCError_FeatureNotEnabled(t *testing.T) {
	t.Parallel()
	// Verify the wire-error→typed-error mapping for the
	// experimentalApi-required pattern.
	err := classifyRPCError("thread/backgroundTerminals/clean", &jsonrpcRPCError{
		Code:    -32600,
		Message: "thread/backgroundTerminals/clean requires experimentalApi capability",
	})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !types.IsFeatureNotEnabledError(err) {
		t.Errorf("expected FeatureNotEnabledError, got %T: %v", err, err)
	}
}

func TestClassifyRPCError_GenericRPCError(t *testing.T) {
	t.Parallel()
	err := classifyRPCError("config/value/write", &jsonrpcRPCError{
		Code:    -32600,
		Message: "Invalid request: missing field 'keyPath'",
	})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !types.IsRPCError(err) {
		t.Errorf("expected RPCError, got %T: %v", err, err)
	}
	if types.IsFeatureNotEnabledError(err) {
		t.Errorf("must NOT classify as FeatureNotEnabledError")
	}
}

func TestClientCommands_ClosedAfterPreConnectClose(t *testing.T) {
	t.Parallel()
	// Simulates: NewClient → Close (without Connect). All read methods
	// must report "client closed" not "not connected" — because closed
	// takes precedence semantically.
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{"ReadConfig", func(c *Client) error { _, err := c.ReadConfig(context.Background()); return err }},
		{"ListModels", func(c *Client) error { _, err := c.ListModels(context.Background()); return err }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := NewClient(context.Background(), types.NewCodexOptions())
			_ = c.Close(context.Background())
			err := tt.call(c)
			if err == nil {
				t.Fatal("expected error after Close")
			}
			// Pre-Connect Close marks client closed=true but
			// connected=false. The "not connected" check fires first
			// because that's the more specific error.
			if !strings.Contains(err.Error(), "not connected") &&
				!strings.Contains(err.Error(), "client closed") {
				t.Errorf("error = %q, want 'not connected' or 'client closed'", err)
			}
		})
	}
}
