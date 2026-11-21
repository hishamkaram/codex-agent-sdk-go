package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ErrThreadExecutionUnconfirmed means another provider operation may still
// own this thread. The SDK sends no new work until it sees matching completion
// or proves no operation started. A fresh idle resume reply can resolve an
// unchanged external-only fence. An unresolved SDK operation still requires
// terminal evidence or client close. A destination with uncertain approved
// plan preparation should be replaced by a fresh thread.
var ErrThreadExecutionUnconfirmed = errors.New("codex: thread execution is unconfirmed")

// ThreadExecutionUnconfirmedError identifies the affected provider thread.
type ThreadExecutionUnconfirmedError struct {
	ThreadID string
}

func (e *ThreadExecutionUnconfirmedError) Error() string {
	return fmt.Sprintf("%v for thread %q", ErrThreadExecutionUnconfirmed, e.ThreadID)
}

func (*ThreadExecutionUnconfirmedError) Unwrap() error { return ErrThreadExecutionUnconfirmed }

func (t *Thread) isClosed() bool { return t.record != nil && t.record.Closed() }

func (t *Thread) beginOperation(ctx context.Context, kind threadexec.Kind, streamed bool, method string, params any) (*threadexec.Operation, jsonrpc.Response, error) {
	if t.isClosed() || t.record == nil {
		return nil, jsonrpc.Response{}, types.ErrThreadClosed
	}
	if err := t.client.checkApprovalReentry(ctx); err != nil {
		return nil, jsonrpc.Response{}, err
	}
	return t.record.Begin(ctx, t.id, kind, streamed, method, params, t.executionWire())
}

func (t *Thread) executionWire() threadexec.Wire {
	id := threadexec.CanonicalID(t.id)
	return threadexec.Wire{
		PostGuarded: t.client.demux.PostGuarded,
		PendingActivity: func() (bool, error) {
			return t.client.demux.CheckPendingNotification(func(n jsonrpc.Notification) bool {
				return pendingActivityAffectsThread(n, id)
			})
		},
		Interrupt: t.client.InterruptThreadTurn,
		Unconfirmed: func(threadID string) error {
			return &ThreadExecutionUnconfirmedError{ThreadID: threadID}
		},
		Warn: func(message string) { t.client.logger.Warn(message) },
	}
}
