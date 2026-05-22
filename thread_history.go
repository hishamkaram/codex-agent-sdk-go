package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ReadThread returns persisted Codex thread state through app-server
// thread/read. It does not resume the thread and does not start a turn.
func (c *Client) ReadThread(ctx context.Context, threadID string, opts *types.ThreadReadOptions) (*types.ThreadReadResult, error) {
	if !c.connected.Load() || c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.ReadThread: client not connected or already closed")
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryInvalidThreadID, threadID, "thread id must not be empty", nil)
	}
	params := map[string]any{
		"threadId": threadID,
	}
	if opts != nil {
		params["includeTurns"] = opts.IncludeTurns
	}
	resp, err := c.demux.Send(ctx, "thread/read", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.ReadThread: %w", err)
	}
	if resp.Error != nil {
		return nil, threadHistoryRPCError("thread/read", threadID, resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	var outer struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := json.Unmarshal(resp.Result, &outer); err != nil {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryMalformed, threadID, "malformed thread/read response", types.NewJSONDecodeError(string(resp.Result), err))
	}
	if len(outer.Thread) == 0 {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryMalformed, threadID, "thread/read response missing thread", nil)
	}
	thread, err := parseThreadRecord(outer.Thread)
	if err != nil {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryMalformed, threadID, "malformed thread/read thread", err)
	}
	return &types.ThreadReadResult{Thread: thread, Raw: cloneRaw(resp.Result)}, nil
}

// ListThreads keeps the original unpaged API and returns the first page of
// app-server thread/list results.
func (c *Client) ListThreads(ctx context.Context) ([]types.ThreadInfo, error) {
	page, err := c.ListThreadsPage(ctx, nil)
	if err != nil {
		return nil, err
	}
	return page.Threads, nil
}

// ListThreadsPage returns a page of persisted thread metadata via thread/list.
func (c *Client) ListThreadsPage(ctx context.Context, opts *types.ThreadListOptions) (*types.ThreadListPage, error) {
	if !c.connected.Load() || c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.ListThreadsPage: client not connected or already closed")
	}
	params := buildThreadListParams(opts)
	resp, err := c.demux.Send(ctx, "thread/list", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.ListThreadsPage: %w", err)
	}
	if resp.Error != nil {
		return nil, threadHistoryRPCError("thread/list", "", resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	var out struct {
		Data            []json.RawMessage `json:"data"`
		Threads         []json.RawMessage `json:"threads"`
		NextCursor      string            `json:"nextCursor"`
		BackwardsCursor string            `json:"backwardsCursor"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryMalformed, "", "malformed thread/list response", types.NewJSONDecodeError(string(resp.Result), err))
	}
	rows := out.Data
	if len(rows) == 0 {
		rows = out.Threads
	}
	infos := make([]types.ThreadInfo, 0, len(rows))
	for _, raw := range rows {
		info, err := parseThreadInfo(raw)
		if err != nil {
			return nil, types.NewThreadHistoryError(types.ThreadHistoryMalformed, "", "malformed thread/list row", err)
		}
		if info.ThreadID == "" {
			continue
		}
		infos = append(infos, info)
	}
	return &types.ThreadListPage{
		Threads:         infos,
		NextCursor:      out.NextCursor,
		BackwardsCursor: out.BackwardsCursor,
		Raw:             cloneRaw(resp.Result),
	}, nil
}

// GetThreadMessages extracts persisted user and assistant messages from
// thread/read includeTurns. It never calls thread/resume, thread/start, or
// turn/start.
func (c *Client) GetThreadMessages(ctx context.Context, threadID string, opts *types.GetThreadMessagesOptions) ([]types.ThreadMessage, error) {
	read, err := c.ReadThread(ctx, threadID, &types.ThreadReadOptions{IncludeTurns: true})
	if err != nil {
		return nil, err
	}
	var out []types.ThreadMessage
	for _, turn := range read.Thread.Turns {
		for _, item := range turn.Items {
			msg, ok := threadMessageFromItem(read.Thread.ID, turn.ID, item)
			if ok {
				out = append(out, msg)
			}
		}
	}
	return applyThreadMessageWindow(out, opts), nil
}

func buildThreadListParams(opts *types.ThreadListOptions) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	params := map[string]any{}
	if opts.Limit > 0 {
		params["limit"] = opts.Limit
	}
	if opts.Cursor != "" {
		params["cursor"] = opts.Cursor
	}
	if opts.Archived != nil {
		params["archived"] = *opts.Archived
	}
	if len(opts.Cwd) == 1 {
		params["cwd"] = opts.Cwd[0]
	} else if len(opts.Cwd) > 1 {
		params["cwd"] = append([]string(nil), opts.Cwd...)
	}
	if opts.SearchTerm != "" {
		params["searchTerm"] = opts.SearchTerm
	}
	if opts.SortKey != "" {
		params["sortKey"] = opts.SortKey
	}
	if opts.SortDirection != "" {
		params["sortDirection"] = opts.SortDirection
	}
	if len(opts.ModelProviders) > 0 {
		params["modelProviders"] = append([]string(nil), opts.ModelProviders...)
	}
	if len(opts.SourceKinds) > 0 {
		params["sourceKinds"] = append([]string(nil), opts.SourceKinds...)
	}
	if opts.UseStateDBOnly {
		params["useStateDbOnly"] = true
	}
	return params
}

func parseThreadInfo(raw json.RawMessage) (types.ThreadInfo, error) {
	var row struct {
		ID            string          `json:"id"`
		SessionID     string          `json:"sessionId"`
		Name          string          `json:"name"`
		Preview       string          `json:"preview"`
		Cwd           string          `json:"cwd"`
		Model         string          `json:"model"`
		ModelProvider json.RawMessage `json:"modelProvider"`
		Path          string          `json:"path"`
		UpdatedAt     string          `json:"updatedAt"`
		CreatedAt     string          `json:"createdAt"`
		Archived      bool            `json:"archived"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return types.ThreadInfo{}, types.NewJSONDecodeError(string(raw), err)
	}
	id := row.ID
	if id == "" {
		id = row.SessionID
	}
	model := row.Model
	if model == "" && len(row.ModelProvider) > 0 {
		model = string(row.ModelProvider)
	}
	lastModified := row.UpdatedAt
	if lastModified == "" {
		lastModified = row.CreatedAt
	}
	if lastModified == "" {
		lastModified = row.Path
	}
	summary := row.Preview
	if summary == "" {
		summary = row.Name
	}
	return types.ThreadInfo{
		ThreadID:     id,
		Summary:      summary,
		LastModified: lastModified,
		Cwd:          row.Cwd,
		Model:        model,
		Archived:     row.Archived,
		Raw:          cloneRaw(raw),
	}, nil
}

func parseThreadRecord(raw json.RawMessage) (types.ThreadRecord, error) {
	var row struct {
		ID            string            `json:"id"`
		SessionID     string            `json:"sessionId"`
		Name          string            `json:"name"`
		Preview       string            `json:"preview"`
		Cwd           string            `json:"cwd"`
		Model         string            `json:"model"`
		ModelProvider json.RawMessage   `json:"modelProvider"`
		Path          string            `json:"path"`
		Status        string            `json:"status"`
		CreatedAt     string            `json:"createdAt"`
		UpdatedAt     string            `json:"updatedAt"`
		Turns         []json.RawMessage `json:"turns"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return types.ThreadRecord{}, types.NewJSONDecodeError(string(raw), err)
	}
	id := row.ID
	if id == "" {
		id = row.SessionID
	}
	model := row.Model
	if model == "" && len(row.ModelProvider) > 0 {
		model = string(row.ModelProvider)
	}
	turns := make([]types.ThreadHistoryTurn, 0, len(row.Turns))
	for _, turnRaw := range row.Turns {
		turn, err := parseThreadHistoryTurn(turnRaw)
		if err != nil {
			return types.ThreadRecord{}, err
		}
		turns = append(turns, turn)
	}
	return types.ThreadRecord{
		ID:        id,
		SessionID: row.SessionID,
		Name:      row.Name,
		Preview:   row.Preview,
		Cwd:       row.Cwd,
		Model:     model,
		Path:      row.Path,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Turns:     turns,
		Raw:       cloneRaw(raw),
	}, nil
}

func parseThreadHistoryTurn(raw json.RawMessage) (types.ThreadHistoryTurn, error) {
	var row struct {
		ID          string            `json:"id"`
		Status      string            `json:"status"`
		StartedAt   string            `json:"startedAt"`
		CompletedAt string            `json:"completedAt"`
		Items       []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return types.ThreadHistoryTurn{}, types.NewJSONDecodeError(string(raw), err)
	}
	items := make([]types.ThreadHistoryItem, 0, len(row.Items))
	for _, itemRaw := range row.Items {
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(itemRaw, &probe); err != nil {
			return types.ThreadHistoryTurn{}, types.NewJSONDecodeError(string(itemRaw), err)
		}
		items = append(items, types.ThreadHistoryItem{
			Type: probe.Type,
			ID:   probe.ID,
			Raw:  cloneRaw(itemRaw),
		})
	}
	return types.ThreadHistoryTurn{
		ID:          row.ID,
		Status:      row.Status,
		StartedAt:   row.StartedAt,
		CompletedAt: row.CompletedAt,
		Items:       items,
		Raw:         cloneRaw(raw),
	}, nil
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

func threadHistoryRPCError(method, threadID string, code int, message string, data []byte) error {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "not found"):
		return types.NewThreadHistoryError(types.ThreadHistoryNotFound, threadID, message, types.NewRPCError(code, message, data))
	case strings.Contains(lower, "unknown method"), strings.Contains(lower, "not supported"), strings.Contains(lower, "unsupported"):
		return types.NewThreadHistoryError(types.ThreadHistoryFeatureUnavailable, threadID, method+" unavailable: "+message, types.NewRPCError(code, message, data))
	default:
		return types.NewRPCError(code, message, data)
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp
}
