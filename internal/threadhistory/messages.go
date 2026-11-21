package threadhistory

import (
	"encoding/json"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Messages extracts the user and assistant messages in the requested window.
func Messages(thread types.ThreadRecord, opts *types.GetThreadMessagesOptions) []types.ThreadMessage {
	var out []types.ThreadMessage
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			msg, ok := threadMessageFromItem(thread.ID, turn.ID, item)
			if ok {
				out = append(out, msg)
			}
		}
	}
	return applyThreadMessageWindow(out, opts)
}

func threadMessageFromItem(threadID, turnID string, item types.ThreadHistoryItem) (types.ThreadMessage, bool) {
	switch item.Type {
	case "userMessage":
		text := userMessageText(item.Raw)
		return types.ThreadMessage{Role: "user", ID: item.ID, ThreadID: threadID, TurnID: turnID, Text: text, Raw: cloneRaw(item.Raw)}, true
	case "agentMessage":
		var msg struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(item.Raw, &msg)
		return types.ThreadMessage{Role: "assistant", ID: item.ID, ThreadID: threadID, TurnID: turnID, Text: msg.Text, Raw: cloneRaw(item.Raw)}, true
	default:
		return types.ThreadMessage{}, false
	}
}

func userMessageText(raw json.RawMessage) string {
	var msg struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}
	parts := make([]string, 0, len(msg.Content))
	for _, part := range msg.Content {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func applyThreadMessageWindow(values []types.ThreadMessage, opts *types.GetThreadMessagesOptions) []types.ThreadMessage {
	if opts == nil {
		return values
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(values) {
		return []types.ThreadMessage{}
	}
	values = values[offset:]
	if opts.Limit > 0 && opts.Limit < len(values) {
		values = values[:opts.Limit]
	}
	return values
}
