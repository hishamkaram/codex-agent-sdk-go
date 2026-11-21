package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
