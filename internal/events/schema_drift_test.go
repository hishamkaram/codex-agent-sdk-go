package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

var vendoredV2Schema = filepath.Join("testdata", "schema", "codex_app_server_protocol.v2.schemas.json")

type schemaDoc struct {
	Definitions map[string]json.RawMessage `json:"definitions"`
}

func readSchemaDoc(t *testing.T, schemaPath string) schemaDoc {
	t.Helper()
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema %s: %v", schemaPath, err)
	}
	var doc schemaDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal schema %s: %v", schemaPath, err)
	}
	return doc
}

func notificationParamsDefinitions(t *testing.T, doc schemaDoc) map[string]string {
	t.Helper()
	raw, ok := doc.Definitions["ServerNotification"]
	if !ok {
		t.Fatal("schema missing definitions.ServerNotification")
	}
	var serverNotification struct {
		OneOf []struct {
			Properties struct {
				Method struct {
					Enum []string `json:"enum"`
				} `json:"method"`
				Params struct {
					Ref string `json:"$ref"`
				} `json:"params"`
			} `json:"properties"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(raw, &serverNotification); err != nil {
		t.Fatalf("unmarshal ServerNotification: %v", err)
	}
	methods := make(map[string]string, len(serverNotification.OneOf))
	for _, variant := range serverNotification.OneOf {
		if len(variant.Properties.Method.Enum) != 1 {
			t.Fatalf("expected one method per notification variant, got %v", variant.Properties.Method.Enum)
		}
		method := variant.Properties.Method.Enum[0]
		if _, duplicate := methods[method]; duplicate {
			t.Fatalf("duplicate notification method %q", method)
		}
		methods[method] = strings.TrimPrefix(variant.Properties.Params.Ref, "#/definitions/")
	}
	return methods
}

func notificationMethods(t *testing.T, schemaPath string) []string {
	t.Helper()
	definitions := notificationParamsDefinitions(t, readSchemaDoc(t, schemaPath))
	methods := make([]string, 0, len(definitions))
	for method := range definitions {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

func threadItemTitles(t *testing.T, schemaPath string) []string {
	t.Helper()
	variants := threadItemVariants(t, readSchemaDoc(t, schemaPath))
	titles := make([]string, 0, len(variants))
	for _, variant := range variants {
		titles = append(titles, variant.Title)
	}
	sort.Strings(titles)
	return titles
}

func TestServerNotificationRoutePolicyCoversVendoredSchema(t *testing.T) {
	t.Parallel()

	// route and conditional are typed parsers with required and optional
	// thread identity respectively. global is an existing typed diagnostic.
	// raw methods preserve payloads as UnknownEvent; a global diagnostic has
	// no thread identity and stays out of client thread event subscribers.
	routePolicy := map[string]string{
		"account/gatewayOAuth/changed":              "raw global",
		"account/login/completed":                   "global",
		"account/rateLimits/updated":                "global",
		"account/updated":                           "global",
		"app/list/updated":                          "global",
		"autoApprovalReview/strictReviewRequired":   "raw thread",
		"command/exec/outputDelta":                  "global",
		"configWarning":                             "global",
		"deprecationNotice":                         "global",
		"error":                                     "global",
		"externalAgentConfig/import/completed":      "global",
		"externalAgentConfig/import/progress":       "global",
		"fs/changed":                                "global",
		"fuzzyFileSearch/sessionCompleted":          "global",
		"fuzzyFileSearch/sessionUpdated":            "global",
		"guardianWarning":                           "route",
		"hook/completed":                            "route",
		"hook/started":                              "route",
		"item/agentMessage/delta":                   "route",
		"item/autoApprovalReview/completed":         "route",
		"item/autoApprovalReview/started":           "route",
		"item/commandExecution/outputDelta":         "route",
		"item/commandExecution/terminalInteraction": "route",
		"item/completed":                            "route",
		"item/fileChange/outputDelta":               "route",
		"item/fileChange/patchUpdated":              "route",
		"item/mcpToolCall/progress":                 "route",
		"item/plan/delta":                           "route",
		"item/reasoning/summaryPartAdded":           "route",
		"item/reasoning/summaryTextDelta":           "route",
		"item/reasoning/textDelta":                  "route",
		"item/started":                              "route",
		"mcpServer/event/stream/notification":       "raw global",
		"mcpServer/oauthLogin/completed":            "conditional",
		"mcpServer/startupStatus/updated":           "conditional",
		"model/rerouted":                            "route",
		"model/safetyBuffering/updated":             "route",
		"model/verification":                        "route",
		"modelProvider/authRecoveryCompleted":       "raw thread",
		"modelProvider/authRecoveryStarted":         "raw thread",
		"process/exited":                            "global",
		"process/outputDelta":                       "global",
		"project/changed":                           "raw global",
		"remoteControl/status/changed":              "global",
		"serverRequest/resolved":                    "route",
		"skills/changed":                            "global",
		"thread/archived":                           "route",
		"thread/attachment/updated":                 "raw thread",
		"thread/closed":                             "route",
		"thread/deleted":                            "route",
		"thread/compacted":                          "route",
		"thread/environment/connected":              "raw thread",
		"thread/environment/disconnected":           "raw thread",
		"thread/goal/cleared":                       "route",
		"thread/goal/updated":                       "route",
		"thread/name/updated":                       "route",
		"thread/prediction/updated":                 "raw thread",
		"thread/project/updated":                    "raw thread",
		"thread/queue/changed":                      "raw thread",
		"thread/readState/changed":                  "raw thread",
		"thread/realtime/closed":                    "route",
		"thread/realtime/error":                     "route",
		"thread/realtime/item/completed":            "raw thread",
		"thread/realtime/item/started":              "raw thread",
		"thread/realtime/item/transcript/delta":     "raw thread",
		"thread/realtime/itemAdded":                 "route",
		"thread/realtime/outputAudio/delta":         "route",
		"thread/realtime/sdp":                       "route",
		"thread/realtime/started":                   "route",
		"thread/realtime/transcript/delta":          "route",
		"thread/realtime/transcript/done":           "route",
		"thread/reverted":                           "raw thread",
		"thread/search/partial":                     "raw global",
		"thread/section/changed":                    "raw thread",
		"thread/settings/updated":                   "route",
		"thread/started":                            "route",
		"thread/status/changed":                     "route",
		"thread/tokenUsage/updated":                 "route",
		"thread/unarchived":                         "route",
		"turn/completed":                            "route",
		"turn/diff/updated":                         "route",
		"turn/moderationMetadata":                   "route",
		"turn/plan/updated":                         "route",
		"turn/started":                              "route",
		"warning":                                   "conditional",
		"windows/worldWritableWarning":              "global",
		"windowsSandbox/setupCompleted":             "global",
	}

	doc := readSchemaDoc(t, vendoredV2Schema)
	methods := notificationParamsDefinitions(t, doc)
	for method, paramsDefinition := range methods {
		switch routePolicy[method] {
		case "route", "conditional", "global":
			if eventParsers[method] == nil && rawEventBuilders[method] == nil {
				t.Errorf("schema notification %q has no typed parser", method)
			}
		case "raw thread", "raw global":
			t.Run(method, func(t *testing.T) {
				checkRawNotificationRoute(t, doc, method, paramsDefinition, routePolicy[method] == "raw thread")
			})
		default:
			t.Errorf("schema notification %q has no parser route policy", method)
		}
	}
	for method := range routePolicy {
		if _, ok := methods[method]; !ok {
			t.Errorf("route policy includes %q but vendored schema does not", method)
		}
	}
}

func checkRawNotificationRoute(t *testing.T, doc schemaDoc, method, paramsDefinition string, threadScoped bool) {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(doc.Definitions[paramsDefinition], &schema); err != nil {
		t.Fatalf("unmarshal params definition %q: %v", paramsDefinition, err)
	}
	_, hasThreadID := schema.Properties["threadId"]
	if hasThreadID != threadScoped {
		t.Fatalf("schema has threadId = %t, expected %t for %q", hasThreadID, threadScoped, method)
	}

	params, wantThreadID := json.RawMessage(`{"data":"diagnostic"}`), ""
	if threadScoped {
		if !slices.Contains(schema.Required, "threadId") {
			t.Fatalf("schema does not require threadId for %q", method)
		}
		params, wantThreadID = json.RawMessage(`{"threadId":"child","data":"opaque"}`), "child"
	}
	ev, err := ParseEvent(jsonrpc.Notification{Method: method, Params: params})
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	raw, ok := ev.(*types.UnknownEvent)
	if !ok || raw.Method != method || raw.ThreadID != wantThreadID || string(raw.Params) != string(params) {
		t.Fatalf("event = %#v, want raw %q with thread ID %q and params %s", ev, method, wantThreadID, params)
	}
}

func TestGeneratedSchemaMatchesVendored(t *testing.T) {
	t.Parallel()
	generatedDir := os.Getenv("CODEX_SCHEMA_DRIFT_DIR")
	if generatedDir == "" {
		t.Skip("set CODEX_SCHEMA_DRIFT_DIR to a codex app-server generate-json-schema output directory")
	}
	generated := filepath.Join(generatedDir, "codex_app_server_protocol.v2.schemas.json")

	assertStringSlicesEqual(t, "ServerNotification methods", notificationMethods(t, vendoredV2Schema), notificationMethods(t, generated))
	assertStringSlicesEqual(t, "ThreadItem variants", threadItemTitles(t, vendoredV2Schema), threadItemTitles(t, generated))
}

func assertStringSlicesEqual(t *testing.T, label string, want, got []string) {
	t.Helper()
	if len(want) == len(got) {
		equal := true
		for i := range want {
			if want[i] != got[i] {
				equal = false
				break
			}
		}
		if equal {
			return
		}
	}
	t.Fatalf("%s drift\nvendored:  %v\ngenerated: %v", label, want, got)
}
