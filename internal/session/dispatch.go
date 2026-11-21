package session

import (
	"context"
	"errors"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/session/approval"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

var errThreadEventIdentityMissing = errors.New("thread-scoped notification is missing required thread ID")

// Dispatch runs the event-routing goroutine. Reads notifications and
// server-initiated requests from the demux and fans them out.
func (s *Session) Dispatch(ctx context.Context, demux *jsonrpc.Demux, done chan struct{}) {
	approvals := s.StartApprovals(ctx, demux)
	defer func() {
		approvals.Cancel()
		s.MarkDisconnected()
		s.sink.Close()
		s.approvals.ClearEvidence()
		s.threadRegistry.CloseRecords()
		approvals.Wait()
		close(done)
	}()

	notifications := demux.Notifications()
	serverRequests := demux.ServerRequests()
	serverMessages := demux.ServerMessages()
	for notifications != nil || serverRequests != nil || serverMessages != nil {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-serverMessages:
			if !ok {
				serverMessages = nil
				continue
			}
			s.routeServerMessage(demux, approvals, message)
		case note, ok := <-notifications:
			if !ok {
				notifications = nil
				continue
			}
			s.HandleNotification(note)
		case sreq, ok := <-serverRequests:
			if !ok {
				serverRequests = nil
				continue
			}
			approvals.Enqueue(sreq)
		}
	}

	if ctx.Err() == nil && s.MarkSourceLost() {
		var cause error
		select {
		case cause = <-demux.LoopError():
		default:
		}
		s.sink.Fail(Gap{Kind: SourceGap, Cause: cause})
	}
}

func (s *Session) routeServerMessage(demux *jsonrpc.Demux, approvals *approval.Worker, message jsonrpc.ServerMessage) {
	switch {
	case message.Notification != nil:
		s.HandleNotification(*message.Notification)
		demux.NotificationApplied(message.Notification)
	case message.Request != nil:
		approvals.Enqueue(*message.Request)
	default:
		message.DeliverOrderedReply()
	}
}

// pendingActivityAffectsThread only examines immutable inbound data. An
// unidentifiable ownership message could belong to any provider thread.
func pendingActivityAffectsThread(n jsonrpc.Notification, threadID string) bool {
	if n.DecodeError != nil {
		return true
	}
	if n.Method != "turn/started" && n.Method != "thread/status/changed" {
		return false
	}
	ev, err := events.ParseEvent(n)
	if err != nil {
		id, _ := events.ThreadIDFromReply(n.Params)
		return id == "" || threadexec.CanonicalID(id) == threadID
	}
	id, _ := events.ThreadIdentity(ev)
	if id == "" {
		return true
	}
	if threadexec.CanonicalID(id) != threadID {
		return false
	}
	switch activity := ev.(type) {
	case *types.TurnStarted:
		return activity.TurnID == "" || threadexec.IsRootTurn(activity.TurnID, activity.RootTurnID)
	case *types.ThreadStatusChanged:
		kind := threadexec.StatusKind(activity.Status)
		return kind == "" || kind == "active"
	default:
		return true
	}
}

// HandleNotification applies one notification in dispatcher order.
func (s *Session) HandleNotification(n jsonrpc.Notification) {
	if n.DecodeError != nil {
		s.threadRegistry.Invalidate("")
		s.approvals.ClearEvidence()
		s.sink.Fail(Gap{Kind: FrameGap, Cause: n.DecodeError})
		s.logger.Warn("decode inbound frame failed", zap.Error(n.DecodeError))
		return
	}

	ev, err := events.ParseEvent(n)
	if err != nil {
		s.approvals.ClearEvidence()
		threadID, _ := events.ThreadIDFromReply(n.Params)
		s.threadRegistry.Invalidate(threadID)
		s.sink.Fail(Gap{Kind: ParseGap, ThreadID: threadID, Method: n.Method, Cause: err})
		s.logger.Warn("parse event failed",
			zap.String("method", n.Method),
			zap.Error(err))
		return
	}
	// An UnknownEvent means codex emitted a notification method the SDK does not
	// recognize — wire-format drift ahead of the SDK. Surface it as telemetry so
	// drift is observable, not silent. (The demux already classifies frame-shape
	// drift; this catches method-name drift one layer up.)
	if _, ok := ev.(*types.UnknownEvent); ok {
		s.opts.ObserverOrNop().OnUnknownMessage(n.Method)
	}
	threadID, identityRequired := events.ThreadIdentity(ev)
	if threadID == "" {
		if identityRequired {
			s.threadRegistry.Invalidate("")
			s.approvals.ClearEvidence()
			s.sink.Fail(Gap{Kind: ParseGap, Method: n.Method, Cause: errThreadEventIdentityMissing})
			s.logger.Warn("thread-scoped notification missing thread ID",
				zap.String("method", n.Method))
			return
		}
		// Global events — configWarning, account/rateLimits/updated, etc.
		// Logged at debug; clients that want them must expose a hook in v1.1.
		s.logger.Debug("unroutable event (no thread_id)",
			zap.String("method", ev.EventMethod()))
		return
	}
	s.approvals.Observe(ev)
	// Record lifecycle evidence before any lossy event delivery.
	if s.threadRegistry.Observe(threadID, ev) {
		s.logger.Warn("operation event queue full, dropped notification",
			zap.String("thread_id", threadID), zap.String("method", n.Method))
	}
	// Unregistered provider-created child threads have no Thread handle, but
	// their events still belong in the client-wide runtime stream.
	s.sink.Publish(threadID, ev)
}

// GapKind distinguishes the existing terminal subscription failures.
type GapKind uint8

const (
	FrameGap GapKind = iota
	ParseGap
	SourceGap
)

type Gap struct {
	Kind     GapKind
	ThreadID string
	Method   string
	Cause    error
}

// EventSink receives synchronous delivery after execution and approval
// evidence is applied. It owns the public queue and error projection.
type EventSink interface {
	Publish(string, types.ThreadEvent)
	Fail(Gap)
	Close()
}

// Threads returns the canonical execution registry for this session.
func (s *Session) Threads() *threadexec.Registry { return s.threadRegistry }

// PendingActivity checks receipts for a provider thread at the writer guard.
func PendingActivity(demux *jsonrpc.Demux, id string) (bool, error) {
	return demux.CheckPendingNotification(func(n jsonrpc.Notification) bool {
		return pendingActivityAffectsThread(n, id)
	})
}

// IsApprovalReentry identifies a callback from this same session.
func (s *Session) IsApprovalReentry(ctx context.Context) bool { return s.approvals.IsReentrant(ctx) }

// StartApprovals starts the callback and denial lanes for this dispatcher.
func (s *Session) StartApprovals(ctx context.Context, demux *jsonrpc.Demux) *approval.Worker {
	return s.approvals.Start(ctx, demux, approval.Options{
		Logger:    s.logger,
		Callback:  func() types.ApprovalCallback { return s.opts.ApprovalCallback },
		Available: func() bool { return !s.Closed() && s.Connected() },
	})
}
