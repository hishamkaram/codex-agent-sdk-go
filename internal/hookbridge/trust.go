package hookbridge

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type hookTrust struct {
	Hash string `json:"trusted_hash"`
}

type hookInventory struct {
	Data []struct {
		Hooks []listedHook `json:"hooks"`
	} `json:"data"`
}

type listedHook struct {
	Key           string              `json:"key"`
	CurrentHash   string              `json:"currentHash"`
	SourcePath    string              `json:"sourcePath"`
	Source        string              `json:"source"`
	HandlerType   string              `json:"handlerType"`
	Command       string              `json:"command"`
	Event         types.HookEventName `json:"eventName"`
	Matcher       string              `json:"matcher"`
	Timeout       int                 `json:"timeoutSec"`
	Async         bool                `json:"async"`
	IsManaged     bool                `json:"isManaged"`
	StatusMessage *string             `json:"statusMessage"`
	ContextLimit  *int                `json:"additionalContextLimit"`
}

// SetTrust records only the exact SDK-generated commands in the live peer's
// hook inventory. Their hashes are rechecked by Codex at thread admission.
func (i *Installation) SetTrust(raw json.RawMessage) error {
	var inventory hookInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		return fmt.Errorf("decode hook inventory: %w", err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	r := &i.resources
	if r.hooksPath == "" {
		return fmt.Errorf("SDK hook installation is closed")
	}
	ownedPath, err := filepath.EvalSymlinks(r.hooksPath)
	if err != nil {
		return fmt.Errorf("resolve generated hooks: %w", err)
	}

	expected := make(map[types.HookEventName]bool, len(callbackEvents))
	for _, event := range callbackEvents {
		expected[types.NormalizeHookEventName(types.HookEventName(event))] = true
	}
	trust := make(map[string]hookTrust, len(expected))
	for _, entry := range inventory.Data {
		for index := range entry.Hooks {
			hook := &entry.Hooks[index]
			if hook.SourcePath != ownedPath && hook.SourcePath != r.hooksPath {
				continue
			}
			if !expected[hook.Event] || !hook.matchesGenerated(r.shimCommand) {
				return fmt.Errorf("provider hook inventory differs from the generated SDK hooks")
			}
			if _, exists := trust[hook.Key]; exists {
				return fmt.Errorf("provider returned a duplicate generated hook key")
			}
			delete(expected, hook.Event)
			trust[hook.Key] = hookTrust{Hash: hook.CurrentHash}
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("provider hook inventory is missing %d generated SDK hooks", len(expected))
	}
	r.trust = trust
	return nil
}

func (h *listedHook) matchesGenerated(command string) bool {
	matcher := hookMatcher
	// These events do not match a tool or source, so the peer omits the
	// effective matcher even if the generated group declares one.
	if h.Event == types.HookUserPromptSubmit || h.Event == types.HookStop {
		matcher = ""
	}
	if h.Source != "user" || h.HandlerType != "command" || h.IsManaged ||
		h.Async || h.Command != command || h.Matcher != matcher || h.Timeout != hooksJSONTimeoutSeconds ||
		h.StatusMessage != nil || h.ContextLimit != nil || !strings.HasPrefix(h.Key, h.SourcePath+":") {
		return false
	}
	digest, ok := strings.CutPrefix(h.CurrentHash, "sha256:")
	if !ok || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// ThreadParams merges the installation's exact hash approvals into session
// flags without mutating caller parameters or persisting any trust state.
func (i *Installation) ThreadParams(params map[string]any) map[string]any {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.resources.trust) == 0 {
		return params
	}
	request := maps.Clone(params)
	config, _ := request["config"].(map[string]any)
	config = maps.Clone(config)
	if config == nil {
		config = make(map[string]any, 1)
	}
	config["hooks.state"] = maps.Clone(i.resources.trust)
	request["config"] = config
	return request
}
