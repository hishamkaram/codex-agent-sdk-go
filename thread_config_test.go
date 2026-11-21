package codex

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestBuildThreadConfigOptions(t *testing.T) {
	t.Parallel()

	defaults := types.NewCodexOptions().
		WithWebSearchMode(types.WebSearchCached).
		WithReasoningEffort("medium").
		WithMCPServers(map[string]types.McpServerConfig{
			"default": types.McpStdioConfig{Command: "default-mcp"},
		})

	if got := buildThreadConfig(types.NewCodexOptions(), threadConfigOverrides{}); got != nil {
		t.Fatalf("unset options produced config: %v", got)
	}
	assertThreadConfigJSON(t, buildThreadConfig(defaults, threadConfigOverrides{}),
		`{"web_search":"cached","model_reasoning_effort":"medium"}`)

	falseValue := false
	creation := threadConfigOverrides{
		webSearchMode:   types.WebSearchDisabled,
		reasoningEffort: "future-model-effort",
		mcpServers: map[string]types.McpServerConfig{
			"custom": types.McpStdioConfig{Command: "local-mcp"},
		},
		workspaceWrite: &workspaceWriteSettings{NetworkAccess: &falseValue, WritableRoots: []string{}},
	}
	assertThreadConfigJSON(t, buildThreadConfig(defaults, creation),
		`{"web_search":"disabled","model_reasoning_effort":"future-model-effort","mcp_servers":{"custom":{"command":"local-mcp"}},"sandbox_workspace_write":{"network_access":false,"writable_roots":[]}}`)
	assertThreadConfigJSON(t, buildThreadConfig(defaults, threadConfigOverrides{reasoningEffort: "low"}),
		`{"web_search":"cached","model_reasoning_effort":"low"}`)
}

func assertThreadConfigJSON(t *testing.T, got any, want string) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", raw, want)
	}
}
