package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// TestSchemaCoverage is the US2 guard against silent schema drift. It loads
// every ThreadItem variant from the vendored v2 schema, enumerates each
// variant's `required` properties, and asserts that the corresponding Go
// struct exposes a matching json tag on some field. If a future codex release
// adds a new required property and the Go type is not updated, this test
// fails with a clear "missing Go field for required property X on item Y"
// error — preventing the class of bug where the item parses successfully but
// the Go struct receives zero values for the new fields.
//
// Scope: only ThreadItem variants. ThreadEvent/ServerRequest/Response use
// anonymous local-struct parsers and do not exhibit the same drift class.
//
// Known exclusions:
//   - the `type` discriminator property is handled by the outer ParseItem
//     switch and is intentionally NOT re-declared on each concrete struct.
//   - schema variants without a registered Go type are logged and skipped
//     (their coverage is added by US3).
func TestSchemaCoverage(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join("testdata", "schema", "codex_app_server_protocol.v2.schemas.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	var doc struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	threadItemRaw, ok := doc.Definitions["ThreadItem"]
	if !ok {
		t.Fatalf("schema missing definitions.ThreadItem")
	}
	var threadItem struct {
		OneOf []struct {
			Title      string   `json:"title"`
			Required   []string `json:"required"`
			Properties struct {
				Type struct {
					Enum []string `json:"enum"`
				} `json:"type"`
			} `json:"properties"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(threadItemRaw, &threadItem); err != nil {
		t.Fatalf("unmarshal ThreadItem: %v", err)
	}
	if len(threadItem.OneOf) == 0 {
		t.Fatalf("ThreadItem.oneOf is empty")
	}

	// Schema title (e.g., "CommandExecutionThreadItem") → Go type name
	// (e.g., "CommandExecution"). The mapping hardcodes the known 16
	// variants rather than relying on a fragile suffix-stripping heuristic.
	// Entries missing from this map are schema variants whose Go type is
	// not yet implemented (US3 adds them); the test skips such variants
	// with a t.Logf so the remaining coverage still runs.
	schemaToGoName := map[string]string{
		"UserMessageThreadItem":         "UserMessage",
		"AgentMessageThreadItem":        "AgentMessage",
		"CommandExecutionThreadItem":    "CommandExecution",
		"FileChangeThreadItem":          "FileChange",
		"McpToolCallThreadItem":         "MCPToolCall",
		"WebSearchThreadItem":           "WebSearch",
		"MemoryReadThreadItem":          "MemoryRead",
		"MemoryWriteThreadItem":         "MemoryWrite",
		"PlanThreadItem":                "Plan",
		"ReasoningThreadItem":           "Reasoning",
		"SystemErrorThreadItem":         "SystemError",
		"HookPromptThreadItem":          "HookPrompt",
		"DynamicToolCallThreadItem":     "DynamicToolCall",
		"CollabAgentToolCallThreadItem": "CollabAgentToolCall",
		"ImageViewThreadItem":           "ImageView",
		"ImageGenerationThreadItem":     "ImageGeneration",
		"EnteredReviewModeThreadItem":   "EnteredReviewMode",
		"ExitedReviewModeThreadItem":    "ExitedReviewMode",
		"ContextCompactionThreadItem":   "ContextCompaction",
	}

	// Go type registry. reflect.TypeOf needs an instance; use typed-nil
	// pointers so the element type is resolvable. Feature 187 US3 extends
	// this registry with the 7 new 0.121.0 item types so schema-coverage
	// asserts completeness across the full v2 ThreadItem union.
	goTypeRegistry := map[string]reflect.Type{
		"UserMessage":         reflect.TypeOf((*types.UserMessage)(nil)).Elem(),
		"AgentMessage":        reflect.TypeOf((*types.AgentMessage)(nil)).Elem(),
		"CommandExecution":    reflect.TypeOf((*types.CommandExecution)(nil)).Elem(),
		"FileChange":          reflect.TypeOf((*types.FileChange)(nil)).Elem(),
		"MCPToolCall":         reflect.TypeOf((*types.MCPToolCall)(nil)).Elem(),
		"WebSearch":           reflect.TypeOf((*types.WebSearch)(nil)).Elem(),
		"MemoryRead":          reflect.TypeOf((*types.MemoryRead)(nil)).Elem(),
		"MemoryWrite":         reflect.TypeOf((*types.MemoryWrite)(nil)).Elem(),
		"Plan":                reflect.TypeOf((*types.Plan)(nil)).Elem(),
		"Reasoning":           reflect.TypeOf((*types.Reasoning)(nil)).Elem(),
		"SystemError":         reflect.TypeOf((*types.SystemError)(nil)).Elem(),
		"HookPrompt":          reflect.TypeOf((*types.HookPrompt)(nil)).Elem(),
		"DynamicToolCall":     reflect.TypeOf((*types.DynamicToolCall)(nil)).Elem(),
		"CollabAgentToolCall": reflect.TypeOf((*types.CollabAgentToolCall)(nil)).Elem(),
		"ImageView":           reflect.TypeOf((*types.ImageView)(nil)).Elem(),
		"ImageGeneration":     reflect.TypeOf((*types.ImageGeneration)(nil)).Elem(),
		"EnteredReviewMode":   reflect.TypeOf((*types.EnteredReviewMode)(nil)).Elem(),
		"ExitedReviewMode":    reflect.TypeOf((*types.ExitedReviewMode)(nil)).Elem(),
		"ContextCompaction":   reflect.TypeOf((*types.ContextCompaction)(nil)).Elem(),
	}

	for _, variant := range threadItem.OneOf {
		variant := variant
		if variant.Title == "" {
			t.Errorf("schema variant missing title: %+v", variant)
			continue
		}
		goName, ok := schemaToGoName[variant.Title]
		if !ok {
			t.Logf("skipping %s — no schema→Go name mapping (handled in US3)", variant.Title)
			continue
		}
		goType, ok := goTypeRegistry[goName]
		if !ok {
			t.Logf("skipping %s — no Go type registered for %s (handled in US3)", variant.Title, goName)
			continue
		}

		// Collect json tags on every Go field (normalized — strip
		// options like ",omitempty").
		gotTags := make(map[string]struct{}, goType.NumField())
		for i := 0; i < goType.NumField(); i++ {
			f := goType.Field(i)
			tag := f.Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			name := tag
			if comma := strings.Index(tag, ","); comma >= 0 {
				name = tag[:comma]
			}
			if name == "" {
				continue
			}
			gotTags[name] = struct{}{}
		}

		for _, propName := range variant.Required {
			// `type` is the wire discriminator; handled by the
			// outer ParseItem switch, not a Go field.
			if propName == "type" {
				continue
			}
			if _, found := gotTags[propName]; !found {
				t.Errorf(
					"missing Go field for required property %q on item %s (schema title: %s)",
					propName, goName, variant.Title,
				)
			}
		}
	}
}
