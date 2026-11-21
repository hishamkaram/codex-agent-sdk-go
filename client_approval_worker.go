package codex

import (
	"context"
	"errors"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/session/approval"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ErrApprovalCallbackReentry means an approval callback attempted to start
// another blocking operation or close its own Client. Return the decision
// before doing work that may depend on approval processing.
var ErrApprovalCallbackReentry = errors.New("codex: blocking client operation in approval callback")

func (c *Client) checkApprovalReentry(ctx context.Context) error {
	if c.approvals.IsReentrant(ctx) {
		return ErrApprovalCallbackReentry
	}
	return nil
}

func (c *Client) startApprovals(ctx context.Context, demux *jsonrpc.Demux) *approval.Worker {
	return c.approvals.Start(ctx, demux, approval.Options{
		Logger:    c.logger,
		Callback:  func() types.ApprovalCallback { return c.opts.ApprovalCallback },
		Available: func() bool { return !c.closed.Load() && c.connected.Load() },
	})
}
