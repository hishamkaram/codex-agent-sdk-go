package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
)

func TestApprovedPlanSchemaRequiresMethodAndBothShapes(t *testing.T) {
	t.Parallel()
	const (
		binding = "ClientRequest.json"
		params  = "v2/ThreadPrepareApprovedPlanParams.json"
		reply   = "v2/ThreadPrepareApprovedPlanResponse.json"
	)
	for _, test := range []struct {
		name, path, old, replacement string
	}{
		{name: "pinned contract"},
		{"missing method", binding, "thread/prepareApprovedPlan", "thread/other"},
		{"wrong parameter binding", binding, "#/definitions/ThreadPrepareApprovedPlanParams", "#/definitions/OtherParams"},
		{"conflicting bound schema", binding, `"markdown":{"type":"string"}`, `"markdown":{"type":"integer"}`},
		{"attachment made required", params, `"required":["markdown","threadId"]`, `"required":["markdown","threadId","attachmentPath"]`},
		{"unexpected required request field", params, `"required":["markdown","threadId"]`, `"required":["markdown","threadId","revision"]`},
		{"attachment no longer nullable", params, `["string","null"]`, `"string"`},
		{"wrong prompt type", reply, `"type":"string"`, `"type":"integer"`},
		{"optional prompt", reply, `"required":["prompt"]`, `"required":[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			schemas := approvedPlanPeerSchemas()
			if test.path != "" {
				original := schemas[test.path]
				schemas[test.path] = strings.Replace(original, test.old, test.replacement, 1)
				if schemas[test.path] == original {
					t.Fatal("fixture mutation did not change its input")
				}
			}
			writeApprovedPlanPeerSchemas(t, root, schemas)
			if got, err := runtimeprobe.SupportsApprovedPlan(root); err != nil || got != (test.path == "") {
				t.Fatalf("schema support = %t, %v", got, err)
			}
		})
	}
}

func writeApprovedPlanPeerSchemas(t *testing.T, root string, schemas map[string]string) {
	t.Helper()
	for path, data := range schemas {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
