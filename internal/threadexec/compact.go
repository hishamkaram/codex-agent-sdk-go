package threadexec

import (
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// CompactWaiter observes the one-shot legacy compact notification.
type CompactWaiter struct {
	record  *Record
	channel *chan *types.ContextCompacted
}

func (e *Record) CompactWaiter(op *Operation) CompactWaiter {
	return CompactWaiter{record: e, channel: op.compactCh}
}

func (w CompactWaiter) Wait(ctx context.Context) (*types.ContextCompacted, error) {
	defer w.detach()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("codex.CompactResult.Wait: %w", ctx.Err())
	case ev, ok := <-*w.channel:
		if !ok {
			return nil, fmt.Errorf("codex.CompactResult.Wait: thread closed before compact completed")
		}
		return ev, nil
	}
}

func (w CompactWaiter) detach() {
	if w.record != nil && w.channel != nil {
		w.record.compactSub.CompareAndSwap(w.channel, nil)
	}
}
