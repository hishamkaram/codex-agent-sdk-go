package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

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

func notificationMethods(t *testing.T, schemaPath string) []string {
	t.Helper()
	doc := readSchemaDoc(t, schemaPath)
	raw, ok := doc.Definitions["ServerNotification"]
	if !ok {
		t.Fatalf("%s missing definitions.ServerNotification", schemaPath)
	}
	var serverNotification struct {
		OneOf []struct {
			Properties struct {
				Method struct {
					Enum []string `json:"enum"`
				} `json:"method"`
			} `json:"properties"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(raw, &serverNotification); err != nil {
		t.Fatalf("unmarshal ServerNotification from %s: %v", schemaPath, err)
	}
	methods := make([]string, 0, len(serverNotification.OneOf))
	for _, variant := range serverNotification.OneOf {
		if len(variant.Properties.Method.Enum) == 0 {
			continue
		}
		methods = append(methods, variant.Properties.Method.Enum[0])
	}
	sort.Strings(methods)
	return methods
}

func threadItemTitles(t *testing.T, schemaPath string) []string {
	t.Helper()
	doc := readSchemaDoc(t, schemaPath)
	raw, ok := doc.Definitions["ThreadItem"]
	if !ok {
		t.Fatalf("%s missing definitions.ThreadItem", schemaPath)
	}
	var threadItem struct {
		OneOf []struct {
			Title string `json:"title"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(raw, &threadItem); err != nil {
		t.Fatalf("unmarshal ThreadItem from %s: %v", schemaPath, err)
	}
	titles := make([]string, 0, len(threadItem.OneOf))
	for _, variant := range threadItem.OneOf {
		if variant.Title != "" {
			titles = append(titles, variant.Title)
		}
	}
	sort.Strings(titles)
	return titles
}

func TestServerNotificationRoutePolicyCoversVendoredSchema(t *testing.T) {
	t.Parallel()

	// route means the event must carry/extract thread_id and be deliverable to
	// Thread.RunStreamed. global means Client.handleNotification intentionally
	// logs/drops it until a future global-event callback exists.
	routePolicy := map[string]string{
		"account/login/completed":                   "global",
		"account/rateLimits/updated":                "global",
		"account/updated":                           "global",
		"app/list/updated":                          "global",
		"command/exec/outputDelta":                  "global",
		"configWarning":                             "global",
		"deprecationNotice":                         "global",
		"error":                                     "global",
		"externalAgentConfig/import/completed":      "global",
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
		"mcpServer/oauthLogin/completed":            "global",
		"mcpServer/startupStatus/updated":           "global",
		"model/rerouted":                            "route",
		"model/verification":                        "route",
		"process/exited":                            "global",
		"process/outputDelta":                       "global",
		"remoteControl/status/changed":              "global",
		"serverRequest/resolved":                    "route",
		"skills/changed":                            "global",
		"thread/archived":                           "route",
		"thread/closed":                             "route",
		"thread/compacted":                          "route",
		"thread/goal/cleared":                       "route",
		"thread/goal/updated":                       "route",
		"thread/name/updated":                       "route",
		"thread/realtime/closed":                    "route",
		"thread/realtime/error":                     "route",
		"thread/realtime/itemAdded":                 "route",
		"thread/realtime/outputAudio/delta":         "route",
		"thread/realtime/sdp":                       "route",
		"thread/realtime/started":                   "route",
		"thread/realtime/transcript/delta":          "route",
		"thread/realtime/transcript/done":           "route",
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

	schemaPath := filepath.Join("testdata", "schema", "codex_app_server_protocol.v2.schemas.json")
	for _, method := range notificationMethods(t, schemaPath) {
		if _, ok := routePolicy[method]; !ok {
			t.Errorf("schema notification %q has no parser route/drop policy", method)
		}
	}
	for method := range routePolicy {
		if !contains(notificationMethods(t, schemaPath), method) {
			t.Errorf("route policy includes %q but vendored schema does not", method)
		}
	}
}

func TestGeneratedSchemaMatchesVendored(t *testing.T) {
	t.Parallel()
	generatedDir := os.Getenv("CODEX_SCHEMA_DRIFT_DIR")
	if generatedDir == "" {
		t.Skip("set CODEX_SCHEMA_DRIFT_DIR to a codex app-server generate-json-schema output directory")
	}
	vendored := filepath.Join("testdata", "schema", "codex_app_server_protocol.v2.schemas.json")
	generated := filepath.Join(generatedDir, "codex_app_server_protocol.v2.schemas.json")

	assertStringSlicesEqual(t, "ServerNotification methods", notificationMethods(t, vendored), notificationMethods(t, generated))
	assertStringSlicesEqual(t, "ThreadItem variants", threadItemTitles(t, vendored), threadItemTitles(t, generated))
}

func contains(items []string, needle string) bool {
	i := sort.SearchStrings(items, needle)
	return i < len(items) && items[i] == needle
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
