// Package threadhistory reads and projects persisted provider thread records.
package threadhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Send sends a history RPC and returns the provider reply for history-specific error mapping.
type Send func(ctx context.Context, method string, params any) (jsonrpc.Response, error)

func ReadThread(ctx context.Context, send Send, threadID string, opts *types.ThreadReadOptions) (*types.ThreadReadResult, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryInvalidThreadID, threadID, "thread id must not be empty", nil)
	}
	params := map[string]any{
		"threadId": threadID,
	}
	if opts != nil {
		params["includeTurns"] = opts.IncludeTurns
	}
	resp, err := send(ctx, "thread/read", params)
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
	thread, parseErr := parseThreadRecord(outer.Thread)
	if parseErr != nil {
		return nil, types.NewThreadHistoryError(types.ThreadHistoryMalformed, threadID, "malformed thread/read thread", parseErr)
	}
	return &types.ThreadReadResult{Thread: thread, Raw: cloneRaw(resp.Result)}, nil
}

func ListThreadsPage(ctx context.Context, send Send, opts *types.ThreadListOptions) (*types.ThreadListPage, error) {
	params := buildThreadListParams(opts)
	resp, err := send(ctx, "thread/list", params)
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
