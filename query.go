package codex

import (
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Query is a fire-and-forget convenience wrapper that creates a Client,
// connects, starts a throwaway thread, runs one turn, and returns the
// streamed events channel. The Client and Thread are closed automatically
// after the channel is drained.
//
// Use this for simple one-off scripts. For multi-turn work, use NewClient
// + StartThread directly so you can reuse the subprocess.
func Query(ctx context.Context, prompt string, opts *types.CodexOptions) (<-chan types.ThreadEvent, error) {
	if opts == nil {
		opts = types.NewCodexOptions()
	}
	client, err := NewClient(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, fmt.Errorf("codex.Query: StartThread: %w", err)
	}
	events, err := thread.RunStreamed(ctx, prompt, nil)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, fmt.Errorf("codex.Query: RunStreamed: %w", err)
	}

	// Wrap the inner channel in a goroutine that forwards events and cleans
	// up the Client when the inner channel closes.
	out := make(chan types.ThreadEvent, ThreadInboxBuffer)
	go func() {
		defer close(out)
		defer func() { _ = client.Close(context.Background()) }()
		for ev := range events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
