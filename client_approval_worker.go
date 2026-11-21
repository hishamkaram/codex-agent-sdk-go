package codex

import (
	"context"
	"errors"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

const (
	approvalQueueSize    = 16
	approvalReplyTimeout = 5 * time.Second
)

// ErrApprovalCallbackReentry means an approval callback attempted to start
// another blocking operation or close its own Client. Return the decision
// before doing work that may depend on approval processing.
var ErrApprovalCallbackReentry = errors.New("codex: blocking client operation in approval callback")

type approvalClientKey struct{}

func (c *Client) checkApprovalReentry(ctx context.Context) error {
	if owner, ok := ctx.Value(approvalClientKey{}).(*Client); ok && owner == c {
		return ErrApprovalCallbackReentry
	}
	return nil
}

type approvalJob struct {
	wire     jsonrpc.ServerRequest
	request  types.ApprovalRequest
	contract types.ApprovalRequest
	callback types.ApprovalCallback
	parseErr *jsonrpc.RPCError
}

// prepareApproval runs on the ordered dispatcher so file context and response
// validation are bound to provider information before user code mutates it.
func (c *Client) prepareApproval(sreq jsonrpc.ServerRequest) approvalJob {
	job := approvalJob{wire: sreq}
	req, err := events.ParseApprovalRequest(sreq.Method, sreq.Params)
	if err != nil {
		c.logger.Warn("parse server-request failed", zap.String("method", sreq.Method))
		job.parseErr = &jsonrpc.RPCError{Code: -32000, Message: "client could not parse approval request parameters"}
		return job
	}
	job.request, job.contract = req, approvalReplyRequest(req)
	job.callback = c.opts.ApprovalCallback
	if file, ok := req.(*types.FileChangeApprovalRequest); ok && !c.resolveFileApproval(file) {
		job.callback = nil
		c.logger.Warn("file approval context unavailable; denying")
	}
	if job.callback == nil {
		job.callback = types.DefaultDenyApprovalCallback
	}
	return job
}

// approvalReplyRequest captures the provider discriminator the encoder reads.
// The callback can edit its request without changing the reply contract.
func approvalReplyRequest(req types.ApprovalRequest) types.ApprovalRequest {
	if elicitation, ok := req.(*types.ElicitationRequest); ok {
		return &types.ElicitationRequest{Mode: elicitation.Mode}
	}
	return req
}

var errApprovalBacklog = errors.New("codex: approval request backlog exhausted")

type approvalLanes struct {
	callbacks chan approvalJob
	denials   chan approvalJob
	cancel    context.CancelCauseFunc
}

func (c *Client) enqueueApproval(ctx context.Context, lanes approvalLanes, request jsonrpc.ServerRequest) {
	job := c.prepareApproval(request)
	select {
	case lanes.callbacks <- job:
		return
	case <-ctx.Done():
		return
	default:
	}
	c.logger.Warn("approval queue full; denying", zap.String("method", request.Method))
	select {
	case lanes.denials <- job:
	case <-ctx.Done():
	default:
		// A second bounded queue guarantees that a blocked writer cannot
		// stop ordered delivery. When both are saturated, fail closed
		// instead of forgetting a request that the peer is waiting for.
		lanes.cancel(errApprovalBacklog)
	}
}

func (c *Client) runApprovalDenials(ctx context.Context, demux *jsonrpc.Demux, jobs <-chan approvalJob, done chan<- struct{}) {
	defer close(done)
	defer func() {
		if errors.Is(context.Cause(ctx), errApprovalBacklog) {
			c.logger.Warn("approval reply backlog exhausted; closing connection")
			demux.Abort(errApprovalBacklog)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jobs:
			if ctx.Err() != nil {
				return
			}
			c.writeApproval(ctx, demux, job, types.ApprovalDeny{})
		}
	}
}

func (c *Client) runApprovalWorker(ctx context.Context, demux *jsonrpc.Demux, jobs <-chan approvalJob, done chan<- struct{}) {
	defer close(done)
	callbackCtx := context.WithValue(ctx, approvalClientKey{}, c)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jobs:
			if ctx.Err() != nil {
				return
			}
			c.completeApproval(callbackCtx, demux, job)
		}
	}
}

func (c *Client) completeApproval(ctx context.Context, demux *jsonrpc.Demux, job approvalJob) {
	var decision types.ApprovalDecision
	if job.parseErr == nil {
		decision = job.callback(ctx, job.request)
		if ctx.Err() != nil {
			if c.closed.Load() || !c.connected.Load() || errors.Is(context.Cause(ctx), errApprovalBacklog) {
				return
			}
			decision = types.ApprovalCancel{}
			// Send a non-accepting reply when the provider can still receive
			// one. The reply has its own deadline and demux shutdown fence.
			ctx = context.WithoutCancel(ctx)
		}
	}
	c.writeApproval(ctx, demux, job, decision)
}

func (c *Client) writeApproval(ctx context.Context, demux *jsonrpc.Demux, job approvalJob, decision types.ApprovalDecision) {
	var result any
	if job.parseErr == nil {
		var err error
		result, err = events.EncodeApprovalDecision(job.contract, decision)
		if err != nil {
			c.logger.Warn("invalid approval decision; denying", zap.String("method", job.wire.Method), zap.Error(err))
		}
	}
	replyCtx, cancel := context.WithTimeout(ctx, approvalReplyTimeout)
	defer cancel()
	err := demux.RespondServerRequestContext(replyCtx, job.wire.ID, result, job.parseErr)
	if err != nil && ctx.Err() == nil && !c.closed.Load() && c.connected.Load() && !errors.Is(err, jsonrpc.ErrClosed) {
		c.logger.Warn("required approval response could not be delivered; closing connection", zap.String("method", job.wire.Method), zap.Error(err))
		demux.Abort(err)
	}
}
