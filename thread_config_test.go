package codex

import (
	"encoding/json"
	"errors"
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

	if got := buildThreadConfig(threadConfigOverrides{}); got != nil {
		t.Fatalf("unset options produced config: %v", got)
	}
	assertThreadConfigJSON(t, buildThreadConfig((threadConfigOverrides{}).withDefaults(defaults)),
		`{"web_search":"cached","model_reasoning_effort":"medium"}`)

	falseValue := false
	creation := threadConfigOverrides{
		webSearchMode:   types.WebSearchDisabled,
		reasoningEffort: "future-model-effort",
		mcpServers: map[string]mcpServerRequest{
			"custom": {Command: "local-mcp"},
		},
		workspaceWrite: &workspaceWriteSettings{NetworkAccess: &falseValue, WritableRoots: []string{}},
	}
	assertThreadConfigJSON(t, buildThreadConfig(creation.withDefaults(defaults)),
		`{"web_search":"disabled","model_reasoning_effort":"future-model-effort","mcp_servers":{"custom":{"command":"local-mcp"}},"sandbox_workspace_write":{"network_access":false,"writable_roots":[]}}`)
	assertThreadConfigJSON(t, buildThreadConfig((threadConfigOverrides{reasoningEffort: "low"}).withDefaults(defaults)),
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

func TestVerifyResumeReasoningEffortReadback(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		reply    string
		expected string
		mismatch bool
	}{
		{"matches", `{"reasoningEffort":"high"}`, "high", false},
		{"legacy reply", `{}`, "high", false},
		{"null", `{"reasoningEffort":null}`, "high", true},
		{"malformed", `{"reasoningEffort":123}`, "high", true},
		{"invalid result type", `[]`, "high", true},
		{"unset request", `{"reasoningEffort":"medium"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyResumeReasoningEffort(json.RawMessage(tc.reply), "resumed", tc.expected)
			var mismatch *types.ThreadResumeSettingsMismatchError
			if tc.mismatch {
				if !errors.As(err, &mismatch) || mismatch.ThreadID != "resumed" {
					t.Fatalf("error = %v, want mismatch for resumed thread", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
