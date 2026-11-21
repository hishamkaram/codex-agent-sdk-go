package codex

import (
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadhistory"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ReadThread returns persisted Codex thread state through app-server
// thread/read. It does not resume the thread and does not start a turn.
func (c *Client) ReadThread(ctx context.Context, threadID string, opts *types.ThreadReadOptions) (*types.ThreadReadResult, error) {
	if c.session.Closed() {
		return nil, fmt.Errorf("codex.Client.ReadThread: %w", types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return nil, fmt.Errorf("codex.Client.ReadThread: %w", types.ErrClientNotConnected)
	}
	return threadhistory.ReadThread(ctx, c.session.Demux().Send, threadID, opts)
}

// ListThreadsPage returns a page of persisted thread metadata via thread/list.
func (c *Client) ListThreadsPage(ctx context.Context, opts *types.ThreadListOptions) (*types.ThreadListPage, error) {
	if c.session.Closed() {
		return nil, fmt.Errorf("codex.Client.ListThreadsPage: %w", types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return nil, fmt.Errorf("codex.Client.ListThreadsPage: %w", types.ErrClientNotConnected)
	}
	return threadhistory.ListThreadsPage(ctx, c.session.Demux().Send, opts)
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

// GetThreadMessages extracts persisted user and assistant messages from
// thread/read includeTurns. It never calls thread/resume, thread/start, or
// turn/start.
func (c *Client) GetThreadMessages(ctx context.Context, threadID string, opts *types.GetThreadMessagesOptions) ([]types.ThreadMessage, error) {
	read, err := c.ReadThread(ctx, threadID, &types.ThreadReadOptions{IncludeTurns: true})
	if err != nil {
		return nil, err
	}
	return threadhistory.Messages(read.Thread, opts), nil
}
