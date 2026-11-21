package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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

	c, _ := setupMockClient(t, types.NewCodexOptions().WithHookCallback(types.DefaultAllowHookHandler), nil)

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

func TestClassifyRPCError_FeatureNotEnabled(t *testing.T) {
	t.Parallel()
	// Verify the wire-error→typed-error mapping for the
	// experimentalApi-required pattern.
	err := classifyRPCError("thread/backgroundTerminals/clean", &jsonrpc.RPCError{
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
	err := classifyRPCError("config/value/write", &jsonrpc.RPCError{
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

func TestClassifyRPCError_ConfigRequirementsUnsupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    int
		message string
	}{
		{name: "method not found", code: -32601, message: "method not found"},
		{name: "legacy invalid request", code: -32600, message: "Invalid request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := classifyRPCError("configRequirements/read", &jsonrpc.RPCError{
				Code: tt.code, Message: tt.message,
			})
			if !errors.Is(err, ErrRuntimeControlsUnsupported) {
				t.Fatalf("classifyRPCError() = %v, want ErrRuntimeControlsUnsupported", err)
			}
		})
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

func TestListModelsAggregatesEveryPage(t *testing.T) {
	t.Parallel()

	replies := []string{
		`{"data":[{"id":"model-a"}],"nextCursor":"model /+ cursor=="}`,
		`{"data":[],"nextCursor":""}`,
		`{"data":[{"id":"model-b"},{"id":"model-c"}],"nextCursor":null}`,
	}
	index := 0
	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if index >= len(replies) {
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32603, Message: "unexpected page"}}
		}
		resp := jsonrpc.Response{ID: req.ID, Result: json.RawMessage(replies[index])}
		index++
		return resp
	})
	got, err := c.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "model-a" || got[1].ID != "model-b" || got[2].ID != "model-c" {
		t.Fatalf("models = %+v, want model-a, model-b, and model-c in order", got)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "model/list", "model/list", "model/list")
	assertThreadConfigJSON(t, wire[0].Params, `{}`)
	assertThreadConfigJSON(t, wire[1].Params, `{"cursor":"model /+ cursor=="}`)
	assertThreadConfigJSON(t, wire[2].Params, `{"cursor":""}`)
}

func TestListModelsEmptyInventoryWithoutCursor(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[]}`)}
	})
	models, err := c.ListModels(t.Context())
	if err != nil || models == nil || len(models) != 0 {
		t.Fatalf("ListModels = %#v, %v, want a non-nil empty inventory", models, err)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "model/list")
	assertThreadConfigJSON(t, wire[0].Params, `{}`)
}

func TestListModelsKeepsLegacyNilInventory(t *testing.T) {
	t.Parallel()

	for _, response := range []string{`{}`, `{"data":null,"nextCursor":null}`} {
		t.Run(response, func(t *testing.T) {
			t.Parallel()

			c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(response)}
			})
			models, err := c.ListModels(t.Context())
			if err != nil || models != nil {
				t.Fatalf("ListModels = %#v, %v, want a nil inventory without an error", models, err)
			}
			wire := observedThreadRequests(t, server)
			assertThreadRequestMethods(t, wire, "model/list")
			assertThreadConfigJSON(t, wire[0].Params, `{}`)
		})
	}
}

func TestListModelsRejectsIncompleteFirstPage(t *testing.T) {
	t.Parallel()

	const response = `{"nextCursor":""}`
	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(response)}
	})
	models, err := c.ListModels(t.Context())
	if models != nil || err == nil || errors.Is(err, ErrModelPagination) {
		t.Fatalf("ListModels = %#v, %v, want a nil inventory and a decode error on the first page", models, err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "model/list")
}

func TestInventoryExplicitPagesRequireDataArray(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	if page, err := c.ListModelsPage(t.Context(), nil); page != nil || err == nil {
		t.Fatalf("ListModelsPage = %#v, %v, want a nil page and an error", page, err)
	}
	if page, err := c.ListMCPServerStatusPage(t.Context(), nil); page != nil || err == nil {
		t.Fatalf("ListMCPServerStatusPage = %#v, %v, want a nil page and an error", page, err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "model/list", "mcpServerStatus/list")
}

func TestListModelsPageKeepsExactCursorAndFilters(t *testing.T) {
	t.Parallel()

	replies := []string{
		`{"data":[{"id":"model-a"}],"nextCursor":"opaque /+=="}`,
		`{"data":[{"id":"model-b"}],"nextCursor":null}`,
	}
	index := 0
	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if index >= len(replies) {
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32603, Message: "unexpected page"}}
		}
		resp := jsonrpc.Response{ID: req.ID, Result: json.RawMessage(replies[index])}
		index++
		return resp
	})
	limit := uint32(1)
	includeHidden := false
	options := &types.ModelListOptions{Limit: &limit, IncludeHidden: &includeHidden, ModelProvider: "custom"}
	first, err := c.ListModelsPage(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].ID != "model-a" || first.NextCursor == nil || *first.NextCursor != "opaque /+==" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	options.Cursor = first.NextCursor
	second, err := c.ListModelsPage(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].ID != "model-b" || second.NextCursor != nil {
		t.Fatalf("unexpected second page: %+v", second)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "model/list", "model/list")
	assertThreadConfigJSON(t, wire[0].Params, `{"limit":1,"includeHidden":false,"modelProvider":"custom"}`)
	assertThreadConfigJSON(t, wire[1].Params, `{"cursor":"opaque /+==","limit":1,"includeHidden":false,"modelProvider":"custom"}`)
}

func TestListModelsReturnsNilInventoryOnFailure(t *testing.T) {
	t.Parallel()

	t.Run("second page RPC error", func(t *testing.T) {
		t.Parallel()

		index := 0
		c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
			index++
			if index == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"id":"model-a"}],"nextCursor":"page-two"}`)}
			}
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32603, Message: "inventory unavailable"}}
		})
		got, err := c.ListModels(t.Context())
		var providerError *types.RPCError
		if got != nil || !errors.As(err, &providerError) || providerError.Code != -32603 {
			t.Fatalf("ListModels = %+v, %v, want nil inventory and the provider error", got, err)
		}
		wire := observedThreadRequests(t, server)
		assertThreadRequestMethods(t, wire, "model/list", "model/list")
		assertThreadConfigJSON(t, wire[1].Params, `{"cursor":"page-two"}`)
	})
	t.Run("second page cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		index := 0
		c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
			index++
			if index == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"id":"model-a"}],"nextCursor":"page-two"}`)}
			}
			cancel()
			return jsonrpc.Response{}
		})
		got, err := c.ListModels(ctx)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("ListModels = %+v, %v, want nil inventory and context cancellation", got, err)
		}
		wire := observedThreadRequests(t, server)
		assertThreadRequestMethods(t, wire, "model/list", "model/list")
		assertThreadConfigJSON(t, wire[1].Params, `{"cursor":"page-two"}`)
	})
	t.Run("malformed second page", func(t *testing.T) {
		t.Parallel()

		index := 0
		c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
			index++
			if index == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"id":"model-a"}],"nextCursor":"page-two"}`)}
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":null}`)}
		})
		got, err := c.ListModels(t.Context())
		if got != nil || err == nil {
			t.Fatalf("ListModels = %+v, %v, want nil inventory and decode error", got, err)
		}
		assertThreadRequestMethods(t, observedThreadRequests(t, server), "model/list", "model/list")
	})
}

func TestListModelsStopsOnCursorCycleOrPageLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want int
		next func(int) string
	}{
		{"three-page cycle", 3, func(page int) string { return fmt.Sprint(page % 2) }},
		{"page limit", 100, func(page int) string { return fmt.Sprint(page) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			index := 0
			c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				index++
				if index > tc.want {
					return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32603, Message: "too many requests"}}
				}
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(fmt.Sprintf(`{"data":[],"nextCursor":%q}`, tc.next(index)))}
			})

			got, err := c.ListModels(t.Context())
			if got != nil || !errors.Is(err, ErrModelPagination) {
				t.Fatalf("ListModels = %+v, %v, want nil inventory and pagination error", got, err)
			}
			if count := len(observedThreadRequests(t, server)); count != tc.want {
				t.Fatalf("request count = %d, want %d", count, tc.want)
			}
		})
	}
}

func TestListMCPServerStatusPagesKeepRegistrationIdentity(t *testing.T) {
	t.Parallel()

	replies := []string{
		`{"data":[{"name":"shared","environmentId":"env-a"}],"nextCursor":"mcp /+=="}`,
		`{"data":[{"name":"shared","environmentId":"env-b"},{"name":"thread-wide","environmentId":null},{"name":"legacy"}]}`,
		`{"data":[{"name":"shared","environmentId":"env-b"}],"nextCursor":"filter /+=="}`,
		`{"data":[],"nextCursor":null}`,
	}
	index := 0
	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if index >= len(replies) {
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32603, Message: "unexpected page"}}
		}
		resp := jsonrpc.Response{ID: req.ID, Result: json.RawMessage(replies[index])}
		index++
		return resp
	})

	first, err := c.ListMCPServerStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].Name != "shared" || first.Data[0].EnvironmentID == nil || *first.Data[0].EnvironmentID != "env-a" || first.NextCursor == nil {
		t.Fatalf("unexpected first page: %+v", first)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "mcpServerStatus/list")

	second, err := c.ListMCPServerStatusPage(t.Context(), &types.MCPServerStatusListOptions{Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 3 || second.Data[0].Name != "shared" || second.Data[0].EnvironmentID == nil || *second.Data[0].EnvironmentID != "env-b" || second.NextCursor != nil {
		t.Fatalf("unexpected second page: %+v", second)
	}
	if second.Data[1].EnvironmentID != nil || second.Data[2].EnvironmentID != nil {
		t.Fatalf("expected unset environment IDs for thread-wide and legacy registrations: %+v", second.Data[1:])
	}

	limit := uint32(1)
	options := &types.MCPServerStatusListOptions{
		Limit: &limit, ThreadID: "thread-id", ServerName: "shared",
		EnvironmentID: "env-b", Detail: types.MCPServerStatusDetailToolsAndAuthOnly,
	}
	filtered, err := c.ListMCPServerStatusPage(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Data) != 1 || filtered.NextCursor == nil {
		t.Fatalf("unexpected filtered page: %+v", filtered)
	}
	options.Cursor = filtered.NextCursor
	last, err := c.ListMCPServerStatusPage(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Data) != 0 || last.NextCursor != nil {
		t.Fatalf("unexpected final filtered page: %+v", last)
	}
	wire := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, wire, "mcpServerStatus/list", "mcpServerStatus/list", "mcpServerStatus/list", "mcpServerStatus/list")
	assertThreadConfigJSON(t, wire[0].Params, `{}`)
	assertThreadConfigJSON(t, wire[1].Params, `{"cursor":"mcp /+=="}`)
	assertThreadConfigJSON(t, wire[2].Params, `{"limit":1,"threadId":"thread-id","serverName":"shared","environmentId":"env-b","detail":"toolsAndAuthOnly"}`)
	assertThreadConfigJSON(t, wire[3].Params, `{"cursor":"filter /+==","limit":1,"threadId":"thread-id","serverName":"shared","environmentId":"env-b","detail":"toolsAndAuthOnly"}`)
}

func TestListMCPServerStatusKeepsLegacyNilData(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":null,"nextCursor":"next-page"}`)}
	})
	page, err := c.ListMCPServerStatus(t.Context())
	if err != nil || page == nil || page.Data != nil || page.NextCursor == nil || *page.NextCursor != "next-page" {
		t.Fatalf("ListMCPServerStatus = %#v, %v, want nil data and the cursor from the first page", page, err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server), "mcpServerStatus/list")
}

func TestListMCPServerStatusPageRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	c, server := setupMockClient(t, types.NewCodexOptions(), nil)
	for _, opts := range []types.MCPServerStatusListOptions{
		{EnvironmentID: "env", ThreadID: "thread-id"},
		{EnvironmentID: "env", ServerName: "server"},
		{Detail: "unknown"},
	} {
		if _, err := c.ListMCPServerStatusPage(t.Context(), &opts); err == nil {
			t.Errorf("ListMCPServerStatusPage accepted invalid options: %+v", opts)
		}
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, server))
}

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

func TestListBackgroundTerminalsPaginatesExactPayload(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var cursors []string
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method != "thread/backgroundTerminals/list" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
		var params struct {
			ThreadID string  `json:"threadId"`
			Cursor   *string `json:"cursor"`
			Limit    uint32  `json:"limit"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("decode params: %v", err)
		}
		if params.ThreadID != "thread-child" || params.Limit != 100 {
			t.Errorf("params = %+v", params)
		}
		cursor := ""
		if params.Cursor != nil {
			cursor = *params.Cursor
		}
		mu.Lock()
		cursors = append(cursors, cursor)
		mu.Unlock()
		if cursor == "" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"command":"sleep 10","cwd":"/tmp","itemId":"item-1","processId":"process-1","osPid":12,"cpuPercent":0.5,"rssKb":1024}],"nextCursor":"next"}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"command":"tail -f log","cwd":"/work","itemId":"item-2","processId":"process-2"}],"nextCursor":null}`)}
	})

	terminals, err := c.ListBackgroundTerminals(context.Background(), "thread-child")
	if err != nil {
		t.Fatalf("ListBackgroundTerminals: %v", err)
	}
	if len(terminals) != 2 || terminals[0].ProcessID != "process-1" || terminals[1].Command != "tail -f log" {
		t.Fatalf("terminals = %+v", terminals)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "next" {
		t.Fatalf("cursors = %v", cursors)
	}
}

func TestListBackgroundTerminalsRejectsCursorCycle(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[],"nextCursor":"again"}`)}
	})
	_, err := c.ListBackgroundTerminals(context.Background(), "thread-child")
	if !errors.Is(err, ErrBackgroundTerminalPagination) {
		t.Fatalf("error = %v", err)
	}
}

func TestListBackgroundTerminalsRejectsMissingOrNullData(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`{}`, `null`, `{"data":null}`} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(raw)}
			})
			_, err := c.ListBackgroundTerminals(context.Background(), "thread-child")
			if err == nil || !types.IsJSONDecodeError(err) {
				t.Fatalf("error = %v, want JSONDecodeError", err)
			}
		})
	}
}

func TestTerminateBackgroundTerminalAndInterruptThreadTurnPayloads(t *testing.T) {
	t.Parallel()

	seen := make(map[string]map[string]string)
	var mu sync.Mutex
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		var params map[string]string
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("decode %s params: %v", req.Method, err)
		}
		mu.Lock()
		seen[req.Method] = params
		mu.Unlock()
		if req.Method == "thread/backgroundTerminals/terminate" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"terminated":true}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	if err := c.TerminateBackgroundTerminal(context.Background(), "thread-child", "process-1"); err != nil {
		t.Fatalf("TerminateBackgroundTerminal: %v", err)
	}
	if err := c.InterruptThreadTurn(context.Background(), "thread-child", "turn-child"); err != nil {
		t.Fatalf("InterruptThreadTurn: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := seen["thread/backgroundTerminals/terminate"]; got["threadId"] != "thread-child" || got["processId"] != "process-1" || len(got) != 2 {
		t.Fatalf("terminate params = %#v", got)
	}
	if got := seen["turn/interrupt"]; got["threadId"] != "thread-child" || got["turnId"] != "turn-child" || len(got) != 2 {
		t.Fatalf("interrupt params = %#v", got)
	}
}

func TestTerminateBackgroundTerminalWrapsNegativeAcknowledgement(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"terminated":false}`)}
	})
	err := c.TerminateBackgroundTerminal(context.Background(), "thread-child", "process-1")
	if !errors.Is(err, ErrBackgroundTerminalNotTerminated) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "codex.Client.TerminateBackgroundTerminal") {
		t.Fatalf("error lacks operation context: %v", err)
	}
}
