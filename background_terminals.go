package codex

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

var (
	// ErrBackgroundTerminalPagination marks a cursor cycle or a response that
	// exceeds the SDK's finite pagination bound.
	ErrBackgroundTerminalPagination    = errors.New("codex background terminal pagination invalid")
	ErrBackgroundTerminalNotTerminated = errors.New("codex background terminal was not terminated")
)

// ListBackgroundTerminals returns a complete live inventory for one provider
// thread. Pagination is bounded and cursor cycles fail closed.
func (c *Client) ListBackgroundTerminals(ctx context.Context, threadID string) ([]types.BackgroundTerminal, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: threadID is required")
	}

	var terminals []types.BackgroundTerminal
	var cursor *string
	seenCursors := make(map[string]struct{})
	seenProcesses := make(map[string]struct{})
	for pageNumber := 0; pageNumber < backgroundTerminalMaxPages; pageNumber++ {
		page, nextCursor, err := c.listBackgroundTerminalsPage(ctx, threadID, cursor)
		if err != nil {
			return nil, err
		}
		for _, terminal := range page {
			if strings.TrimSpace(terminal.ProcessID) == "" || strings.TrimSpace(terminal.ItemID) == "" {
				return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: malformed terminal row")
			}
			if _, exists := seenProcesses[terminal.ProcessID]; exists {
				return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: duplicate process %q: %w", terminal.ProcessID, ErrBackgroundTerminalPagination)
			}
			seenProcesses[terminal.ProcessID] = struct{}{}
			terminals = append(terminals, terminal)
		}
		if nextCursor == nil || *nextCursor == "" {
			return terminals, nil
		}
		if _, exists := seenCursors[*nextCursor]; exists {
			return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: cursor cycle at %q: %w", *nextCursor, ErrBackgroundTerminalPagination)
		}
		seenCursors[*nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return nil, fmt.Errorf("codex.Client.ListBackgroundTerminals: exceeded %d pages: %w", backgroundTerminalMaxPages, ErrBackgroundTerminalPagination)
}

func (c *Client) listBackgroundTerminalsPage(ctx context.Context, threadID string, cursor *string) ([]types.BackgroundTerminal, *string, error) {
	params := struct {
		ThreadID string  `json:"threadId"`
		Cursor   *string `json:"cursor,omitempty"`
		Limit    uint32  `json:"limit"`
	}{ThreadID: threadID, Cursor: cursor, Limit: backgroundTerminalPageSize}
	raw, err := c.sendRaw(ctx, "ListBackgroundTerminals", "thread/backgroundTerminals/list", params)
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

// TerminateBackgroundTerminal asks the provider to terminate exactly one
// process in one thread. A nil error is an acknowledgement, not lifecycle
// evidence; callers that expose state must wait for a fresh inventory without
// the process or a correlated terminal event.
func (c *Client) TerminateBackgroundTerminal(ctx context.Context, threadID, processID string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(processID) == "" {
		return fmt.Errorf("codex.Client.TerminateBackgroundTerminal: threadID and processID are required")
	}
	raw, err := c.sendRaw(ctx, "TerminateBackgroundTerminal", "thread/backgroundTerminals/terminate", map[string]string{
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
		return fmt.Errorf("codex.Client.TerminateBackgroundTerminal: %w", ErrBackgroundTerminalNotTerminated)
	}
	return nil
}

// CleanBackgroundTerminals asks the provider to stop every background terminal
// in one thread. A nil error is an acknowledgement; callers must observe a
// fresh empty inventory before reporting that the terminals stopped.
func (c *Client) CleanBackgroundTerminals(ctx context.Context, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("codex.Client.CleanBackgroundTerminals: threadID is required")
	}
	_, err := c.sendRaw(ctx, "CleanBackgroundTerminals", "thread/backgroundTerminals/clean", map[string]string{"threadId": threadID})
	return err
}

// InterruptThreadTurn asks the provider to interrupt the exact turn on the
// exact thread. The spawning parent may remain blocked waiting for that child;
// callers must observe both child and parent lifecycle before presenting this
// as delegated-agent cancellation.
func (c *Client) InterruptThreadTurn(ctx context.Context, threadID, turnID string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("codex.Client.InterruptThreadTurn: threadID and turnID are required")
	}
	_, err := c.sendRaw(ctx, "InterruptThreadTurn", "turn/interrupt", map[string]string{
		"threadId": threadID,
		"turnId":   turnID,
	})
	return err
}
