package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const (
	backgroundTerminalPageSize = uint32(100)
	backgroundTerminalMaxPages = 100
)

func ListBackgroundTerminals(ctx context.Context, send Send, threadID string, paginationError error) ([]types.BackgroundTerminal, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: threadID is required")
	}

	var terminals []types.BackgroundTerminal
	var cursor *string
	seenCursors := make(map[string]struct{})
	seenProcesses := make(map[string]struct{})
	for pageNumber := 0; pageNumber < backgroundTerminalMaxPages; pageNumber++ {
		page, nextCursor, err := listBackgroundTerminalsPage(ctx, send, threadID, cursor)
		if err != nil {
			return nil, err
		}
		for _, terminal := range page {
			if strings.TrimSpace(terminal.ProcessID) == "" || strings.TrimSpace(terminal.ItemID) == "" {
				return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: malformed terminal row")
			}
			if _, exists := seenProcesses[terminal.ProcessID]; exists {
				return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: duplicate process %q: %w", terminal.ProcessID, paginationError)
			}
			seenProcesses[terminal.ProcessID] = struct{}{}
			terminals = append(terminals, terminal)
		}
		if nextCursor == nil || *nextCursor == "" {
			return terminals, nil
		}
		if _, exists := seenCursors[*nextCursor]; exists {
			return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: cursor cycle at %q: %w", *nextCursor, paginationError)
		}
		seenCursors[*nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: exceeded %d pages: %w", backgroundTerminalMaxPages, paginationError)
}

func listBackgroundTerminalsPage(ctx context.Context, send Send, threadID string, cursor *string) ([]types.BackgroundTerminal, *string, error) {
	params := struct {
		ThreadID string  `json:"threadId"`
		Cursor   *string `json:"cursor,omitempty"`
		Limit    uint32  `json:"limit"`
	}{ThreadID: threadID, Cursor: cursor, Limit: backgroundTerminalPageSize}
	raw, err := send(ctx, "ListBackgroundTerminals", "thread/backgroundTerminals/list", params)
	if err != nil {
		return nil, nil, err
	}
	var response struct {
		Data       *[]types.BackgroundTerminal `json:"data"`
		NextCursor *string                     `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: decode response: %w", types.NewJSONDecodeError(string(raw), err))
	}
	if response.Data == nil {
		err := errors.New(`required field "data" must be a non-null array`)
		return nil, nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: decode response: %w", types.NewJSONDecodeError(string(raw), err))
	}
	return *response.Data, response.NextCursor, nil
}

func TerminateBackgroundTerminal(ctx context.Context, send Send, threadID, processID string, notTerminatedError error) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(processID) == "" {
		return fmt.Errorf("codex.Client.TerminateBackgroundTerminal: threadID and processID are required")
	}
	raw, err := send(ctx, "TerminateBackgroundTerminal", "thread/backgroundTerminals/terminate", map[string]string{
		"threadId":  threadID,
		"processId": processID,
	})
	if err != nil {
		return err
	}
	var response struct {
		Terminated bool `json:"terminated"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("codex.Client.TerminateBackgroundTerminal: decode response: %w", types.NewJSONDecodeError(string(raw), err))
	}
	if !response.Terminated {
		return fmt.Errorf("codex.Client.TerminateBackgroundTerminal: %w", notTerminatedError)
	}
	return nil
}

func CleanBackgroundTerminals(ctx context.Context, send Send, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("codex.Client.CleanBackgroundTerminals: threadID is required")
	}
	_, err := send(ctx, "CleanBackgroundTerminals", "thread/backgroundTerminals/clean", map[string]string{"threadId": threadID})
	return err
}

func InterruptThreadTurn(ctx context.Context, send Send, threadID, turnID string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("codex.Client.InterruptThreadTurn: threadID and turnID are required")
	}
	_, err := send(ctx, "InterruptThreadTurn", "turn/interrupt", map[string]string{
		"threadId": threadID,
		"turnId":   turnID,
	})
	return err
}
