// Package approval handles approval evidence and replies for one SDK session.
package approval

import (
	"context"
	"errors"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

const (
	queueSize    = 16
	replyTimeout = 5 * time.Second
)

var errApprovalBacklog = errors.New("codex: approval request backlog exhausted")

type callbackOwnerKey struct{}

// IsReentrant reports whether the context came from a callback on this owner.
func (o *Owner) IsReentrant(ctx context.Context) bool {
	owner, ok := ctx.Value(callbackOwnerKey{}).(*Owner)
	return ok && owner == o
}

// Options supplies values and read access from the owning session.
type Options struct {
	Logger *sdklog.Logger
	// Callback is read by the dispatcher when it captures a request.
	Callback func() types.ApprovalCallback
	// Available reports whether the session can still receive a reply.
	Available func() bool
}

// Worker owns both approval lanes. The dispatcher owns cancellation and join.
type Worker struct {
	owner         *Owner
	demux         *jsonrpc.Demux
	options       Options
	ctx           context.Context
	cancel        context.CancelCauseFunc
	callbacks     chan approvalJob
	denials       chan approvalJob
	callbacksDone chan struct{}
	denialsDone   chan struct{}
}

// Start starts the bounded callback and overflow denial workers.
func (o *Owner) Start(ctx context.Context, demux *jsonrpc.Demux, options Options) *Worker {
	workerCtx, cancel := context.WithCancelCause(ctx)
	worker := &Worker{
		owner:         o,
		demux:         demux,
		options:       options,
		ctx:           workerCtx,
		cancel:        cancel,
		callbacks:     make(chan approvalJob, queueSize),
		denials:       make(chan approvalJob, queueSize),
		callbacksDone: make(chan struct{}),
		denialsDone:   make(chan struct{}),
	}
	go worker.runCallbacks(workerCtx)
	go worker.runDenials(workerCtx)
	return worker
}

// Cancel asks both lanes to stop.
func (w *Worker) Cancel() { w.cancel(nil) }

// Wait joins the two lanes after cancellation.
func (w *Worker) Wait() {
	<-w.denialsDone
	<-w.callbacksDone
}

type approvalJob struct {
	wire     jsonrpc.ServerRequest
	request  types.ApprovalRequest
	contract types.ApprovalRequest
	callback types.ApprovalCallback
	parseErr *jsonrpc.RPCError
}

// prepare runs on the ordered dispatcher so file context and response
// validation are bound to provider information before user code mutates it.
func (w *Worker) prepare(sreq jsonrpc.ServerRequest) approvalJob {
	job := approvalJob{wire: sreq}
	req, err := events.ParseApprovalRequest(sreq.Method, sreq.Params)
	if err != nil {
		w.options.Logger.Warn("parse server-request failed", zap.String("method", sreq.Method))
		job.parseErr = &jsonrpc.RPCError{Code: -32000, Message: "client could not parse approval request parameters"}
		return job
	}
	job.request, job.contract = req, approvalReplyRequest(req)
	job.callback = w.options.Callback()
	if file, ok := req.(*types.FileChangeApprovalRequest); ok && !w.owner.resolveFileApproval(file) {
		job.callback = nil
		w.options.Logger.Warn("file approval context unavailable; denying")
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

// Enqueue captures request evidence before the ordered dispatcher proceeds.
func (w *Worker) Enqueue(request jsonrpc.ServerRequest) {
	job := w.prepare(request)
	select {
	case w.callbacks <- job:
		return
	case <-w.ctx.Done():
		return
	default:
	}
	w.options.Logger.Warn("approval queue full; denying", zap.String("method", request.Method))
	select {
	case w.denials <- job:
	case <-w.ctx.Done():
	default:
		// A second bounded queue guarantees that a blocked writer cannot
		// stop ordered delivery. When both are saturated, fail closed
		// instead of forgetting a request that the peer is waiting for.
		w.cancel(errApprovalBacklog)
	}
}

func (w *Worker) runDenials(ctx context.Context) {
	defer close(w.denialsDone)
	defer func() {
		if errors.Is(context.Cause(ctx), errApprovalBacklog) {
			w.options.Logger.Warn("approval reply backlog exhausted; closing connection")
			w.demux.Abort(errApprovalBacklog)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.denials:
			if ctx.Err() != nil {
				return
			}
			w.reply(ctx, job, types.ApprovalDeny{})
		}
	}
}

func (w *Worker) runCallbacks(ctx context.Context) {
	defer close(w.callbacksDone)
	callbackCtx := context.WithValue(ctx, callbackOwnerKey{}, w.owner)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.callbacks:
			if ctx.Err() != nil {
				return
			}
			w.complete(callbackCtx, job)
		}
	}
}

func (w *Worker) complete(ctx context.Context, job approvalJob) {
	var decision types.ApprovalDecision
	if job.parseErr == nil {
		decision = job.callback(ctx, job.request)
		if ctx.Err() != nil {
			if !w.options.Available() || errors.Is(context.Cause(ctx), errApprovalBacklog) {
				return
			}
			decision = types.ApprovalCancel{}
			// Send a non-accepting reply when the provider can still receive
			// one. The reply has its own deadline and demux shutdown fence.
			ctx = context.WithoutCancel(ctx)
		}
	}
	w.reply(ctx, job, decision)
}

func (w *Worker) reply(ctx context.Context, job approvalJob, decision types.ApprovalDecision) {
	var result any
	if job.parseErr == nil {
		var err error
		result, err = events.EncodeApprovalDecision(job.contract, decision)
		if err != nil {
			w.options.Logger.Warn("invalid approval decision; denying", zap.String("method", job.wire.Method), zap.Error(err))
		}
	}
	replyCtx, cancel := context.WithTimeout(ctx, replyTimeout)
	defer cancel()
	err := w.demux.RespondServerRequestContext(replyCtx, job.wire.ID, result, job.parseErr)
	if err != nil && ctx.Err() == nil && w.options.Available() && !errors.Is(err, jsonrpc.ErrClosed) {
		w.options.Logger.Warn("required approval response could not be delivered; closing connection", zap.String("method", job.wire.Method), zap.Error(err))
		w.demux.Abort(err)
	}
}
