package hookbridge

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestInstallationApprovesOnlyGeneratedHookDeclarations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := (&installationResources{}).installIsolatedHooksJSON(filepath.Dir(path), "/fixture/sdk-shim"); err != nil {
		t.Fatal(err)
	}
	generated := fixtureListedHooks(path)
	unrelated := generated[0]
	unrelated.Key = "project-command"
	unrelated.SourcePath = filepath.Join(filepath.Dir(path), "project", "hooks.json")
	unrelated.Source = "project"
	unrelated.Command = "/fixture/unrelated"
	rows := append(generated, unrelated)
	installation := Installation{resources: installationResources{hooksPath: path, shimCommand: "/fixture/sdk-shim"}}
	if err := installation.SetTrust(marshalHookInventory(t, rows)); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"model_reasoning_effort": "medium"}
	params := map[string]any{"threadId": "thread", "config": config}
	out, err := installation.ThreadParams(params)
	if err != nil {
		t.Fatal(err)
	}
	overrides, ok := out["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %v, want configuration object", out["config"])
	}
	want := make(map[string]hookTrust, len(generated))
	for index := range generated {
		hook := &generated[index]
		want[hook.Key] = hookTrust{Hash: hook.CurrentHash}
	}
	if !reflect.DeepEqual(overrides["hooks.state"], want) || overrides["model_reasoning_effort"] != "medium" {
		t.Fatalf("config = %v, want only generated hashes and original model setting", overrides)
	}
	if params, err := installation.ThreadParams(map[string]any{"config": "invalid"}); params != nil || err == nil {
		t.Fatalf("invalid configuration returned params=%v, err=%v", params, err)
	}
	if len(config) != 1 {
		t.Fatalf("input request configuration was changed: %v", config)
	}
}

func TestInstallationRejectsChangedOrIncompleteHookInventory(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		edit func([]listedHook) []listedHook
	}{
		{"changed command", func(h []listedHook) []listedHook { h[0].Command = "/fixture/changed"; return h }},
		{"key for another file", func(h []listedHook) []listedHook { h[0].Key = "/other/hooks.json:pre_tool_use:0:0"; return h }},
		{"invalid hash", func(h []listedHook) []listedHook { h[0].CurrentHash = "sha256:invalid"; return h }},
		{"unexpected duplicate", func(h []listedHook) []listedHook { return append(h, h[0]) }},
		{"missing owned hook", func(h []listedHook) []listedHook { return h[1:] }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "hooks.json")
			if err := (&installationResources{}).installIsolatedHooksJSON(dir, "/fixture/sdk-shim"); err != nil {
				t.Fatal(err)
			}
			installation := Installation{resources: installationResources{hooksPath: path, shimCommand: "/fixture/sdk-shim"}}
			if err := installation.SetTrust(marshalHookInventory(t, tt.edit(fixtureListedHooks(path)))); err == nil {
				t.Fatal("unexpected approval")
			}
			if params, err := installation.ThreadParams(map[string]any{}); err != nil || len(params) != 0 {
				t.Fatalf("invalid inventory attached state: %v", params)
			}
		})
	}
}

func fixtureListedHooks(path string) []listedHook {
	events := []types.HookEventName{types.HookPreToolUse, types.HookPostToolUse, types.HookSessionStart, types.HookUserPromptSubmit, types.HookStop}
	rows := make([]listedHook, 0, len(events))
	for _, event := range events {
		matcher := ".*"
		if event == types.HookUserPromptSubmit || event == types.HookStop {
			matcher = ""
		}
		rows = append(rows, listedHook{
			Key: path + ":" + string(event) + ":0:0", CurrentHash: "sha256:" + strings.Repeat("f", 64),
			SourcePath: path, Source: "user", HandlerType: "command",
			Command: "/fixture/sdk-shim", Event: event, Matcher: matcher, Timeout: 30,
		})
	}
	return rows
}

func marshalHookInventory(t *testing.T, rows []listedHook) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"data": []map[string]any{{"hooks": rows}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
