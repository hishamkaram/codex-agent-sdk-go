package threadhistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const listRollbackTurnsMethod = "thread/turns/list"

type rollbackListParams struct {
	ThreadID      string `json:"threadId"`
	SortDirection string `json:"sortDirection"`
	ItemsView     string `json:"itemsView"`
	Limit         int    `json:"limit"`
	Cursor        string `json:"cursor,omitempty"`
}

type rollbackListPage struct {
	Data []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
	NextCursor string `json:"nextCursor"`
}

// RollbackBoundary identifies the count-th newest persisted turn. Codex
// excludes this turn and the newer suffix when reverting history.
func RollbackBoundary(ctx context.Context, send Send, threadID string, count int) (string, error) {
	const pageLimit = 100
	if count < 1 {
		return "", errors.New("rollback requires a positive turn count")
	}
	params := rollbackListParams{ThreadID: threadID, SortDirection: "desc", ItemsView: "notLoaded"}
	seenCursors := make(map[string]struct{})
	seenTurns := make(map[string]struct{})
	for found := 0; ; {
		params.Limit = min(count-found, pageLimit)
		page, err := readRollbackTurns(ctx, send, params, seenTurns)
		if err != nil {
			return "", err
		}
		found += len(page.Data)
		if found == count {
			return page.Data[len(page.Data)-1].ID, nil
		}
		if page.NextCursor == "" {
			return "", fmt.Errorf("cannot roll back %d turns: only %d persisted", count, found)
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate || len(page.Data) == 0 {
			return "", errors.New("thread/turns/list pagination did not advance")
		}
		seenCursors[page.NextCursor] = struct{}{}
		params.Cursor = page.NextCursor
	}
}

func readRollbackTurns(ctx context.Context, send Send, params rollbackListParams, seenTurns map[string]struct{}) (rollbackListPage, error) {
	response, err := send(ctx, listRollbackTurnsMethod, params)
	if err != nil {
		return rollbackListPage{}, fmt.Errorf("%s: %w", listRollbackTurnsMethod, err)
	}
	if response.Error != nil {
		wire := response.Error
		return rollbackListPage{}, threadHistoryRPCError(listRollbackTurnsMethod, params.ThreadID, wire.Code, wire.Message, wire.Data)
	}
	var page rollbackListPage
	if err := json.Unmarshal(response.Result, &page); err != nil {
		return rollbackListPage{}, fmt.Errorf("decode %s: %w", listRollbackTurnsMethod, err)
	}
	if page.Data == nil || len(page.Data) > params.Limit {
		return rollbackListPage{}, errors.New("thread/turns/list returned an invalid data page")
	}
	for _, turn := range page.Data {
		if strings.TrimSpace(turn.ID) == "" {
			return rollbackListPage{}, errors.New("thread/turns/list returned a turn without an ID")
		}
		switch turn.Status {
		case "completed", "failed", "interrupted":
		default:
			return rollbackListPage{}, errors.New("thread/turns/list returned a turn without a terminal status")
		}
		if _, duplicate := seenTurns[turn.ID]; duplicate {
			return rollbackListPage{}, errors.New("thread/turns/list returned a duplicate turn")
		}
		seenTurns[turn.ID] = struct{}{}
	}
	return page, nil
}
