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
	thread, startErr := client.StartThread(ctx, nil)
	if startErr != nil {
		// Detach cancellation for best-effort cleanup: the parent ctx may
		// already be canceled (that can be why StartThread failed), but
		// Close must still tear down the subprocess. Inherit values only.
		_ = client.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("codex.Query: StartThread: %w", startErr)
	}
	events, runErr := thread.RunStreamed(ctx, prompt, nil)
	if runErr != nil {
		_ = client.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("codex.Query: RunStreamed: %w", runErr)
	}

	// Wrap the inner channel in a goroutine that forwards events and cleans
	// up the Client when the inner channel closes.
	out := make(chan types.ThreadEvent, ThreadInboxBuffer)
	go func() {
		defer close(out)
		defer func() { _ = client.Close(context.WithoutCancel(ctx)) }()
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
