package codex

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

// dispatch runs the event-routing goroutine. Reads notifications and
// server-initiated requests from the demux and fans them out.
func (c *Client) dispatch(ctx context.Context, demux *jsonrpc.Demux, done chan struct{}) {
	approvals := c.startApprovals(ctx, demux)
	defer func() {
		approvals.Cancel()
		c.connected.Store(false)
		c.closeThreadEventSubscriptions()
		c.approvals.ClearEvidence()
		c.threadRegistry.CloseRecords()
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
			c.routeServerMessage(demux, approvals, message)
		case note, ok := <-notifications:
			if !ok {
				notifications = nil
				continue
			}
			c.handleNotification(note)
		case sreq, ok := <-serverRequests:
			if !ok {
				serverRequests = nil
				continue
			}
			approvals.Enqueue(sreq)
		}
	}

	if ctx.Err() == nil && !c.closed.Load() {
		// Publish disconnected state before the terminal gap so a consumer that
		// reacts immediately cannot register a subscription on a dead source.
		c.connected.Store(false)
		var cause error
		select {
		case cause = <-demux.LoopError():
		default:
		}
		c.failThreadEventSubscriptions(ThreadEventEnvelope{
			Err: &ThreadEventSourceGapError{Cause: cause},
		})
	}
}

func (c *Client) routeServerMessage(demux *jsonrpc.Demux, approvals *approval.Worker, message jsonrpc.ServerMessage) {
	switch {
	case message.Notification != nil:
		c.handleNotification(*message.Notification)
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
		id, _ := extractThreadID(n.Params)
		return id == "" || threadexec.CanonicalID(id) == threadID
	}
	id, _ := threadIdentityFromEvent(ev)
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

func (c *Client) handleNotification(n jsonrpc.Notification) {
	if n.DecodeError != nil {
		c.threadRegistry.Invalidate("")
		c.approvals.ClearEvidence()
		c.failThreadEventSubscriptions(ThreadEventEnvelope{
			Err: &ThreadEventFrameGapError{Cause: n.DecodeError},
		})
		c.logger.Warn("decode inbound frame failed", zap.Error(n.DecodeError))
		return
	}

	ev, err := events.ParseEvent(n)
	if err != nil {
		c.approvals.ClearEvidence()
		threadID, _ := extractThreadID(n.Params)
		c.threadRegistry.Invalidate(threadID)
		c.failThreadEventSubscriptions(ThreadEventEnvelope{
			ThreadID: threadID,
			Err: &ThreadEventParseGapError{
				ThreadID: threadID,
				Method:   n.Method,
				Cause:    err,
			},
		})
		c.logger.Warn("parse event failed",
			zap.String("method", n.Method),
			zap.Error(err))
		return
	}
	// An UnknownEvent means codex emitted a notification method the SDK does not
	// recognize — wire-format drift ahead of the SDK. Surface it as telemetry so
	// drift is observable, not silent. (The demux already classifies frame-shape
	// drift; this catches method-name drift one layer up.)
	if _, ok := ev.(*types.UnknownEvent); ok {
		c.opts.ObserverOrNop().OnUnknownMessage(n.Method)
	}
	threadID, identityRequired := threadIdentityFromEvent(ev)
	if threadID == "" {
		if identityRequired {
			c.threadRegistry.Invalidate("")
			c.approvals.ClearEvidence()
			c.failThreadEventSubscriptions(ThreadEventEnvelope{
				Err: &ThreadEventParseGapError{
					Method: n.Method,
					Cause:  errThreadEventIdentityMissing,
				},
			})
			c.logger.Warn("thread-scoped notification missing thread ID",
				zap.String("method", n.Method))
			return
		}
		// Global events — configWarning, account/rateLimits/updated, etc.
		// Logged at debug; clients that want them must expose a hook in v1.1.
		c.logger.Debug("unroutable event (no thread_id)",
			zap.String("method", ev.EventMethod()))
		return
	}
	c.approvals.Observe(ev)
	// Record lifecycle evidence before any lossy event delivery.
	if c.threadRegistry.Observe(threadID, ev) {
		c.logger.Warn("operation event queue full, dropped notification",
			zap.String("thread_id", threadID), zap.String("method", n.Method))
	}
	// Unregistered provider-created child threads have no Thread handle, but
	// their events still belong in the client-wide runtime stream.
	c.publishThreadEvent(ThreadEventEnvelope{ThreadID: threadID, Event: ev})
}

func (c *Client) registerThread(t *Thread) {
	t.record = c.threadRegistry.Register(t.id, t.record, c.connected.Load)
}

type idleResumeProof struct {
	execution  *threadexec.Record
	generation uint64
}

func (c *Client) requestThreadResume(ctx context.Context, params map[string]any, execution *threadexec.Record) (jsonrpc.Response, idleResumeProof, error) {
	generation, eligible := execution.ResumeIdleCandidate()
	if eligible && c.demux.ServerMessages() != nil {
		reply, err := c.demux.SendOrdered(ctx, "thread/resume", params)
		return reply, idleResumeProof{execution: execution, generation: generation}, err
	}
	reply, err := c.demux.Send(ctx, "thread/resume", params)
	return reply, idleResumeProof{}, err
}

func (p idleResumeProof) reconcile(status, statusThreadID, reportedID, requestedID string) {
	if p.execution != nil && status == "idle" && threadexec.CanonicalID(statusThreadID) == requestedID && threadexec.CanonicalID(reportedID) == requestedID {
		p.execution.ReconcileIdleResume(p.generation)
	}
}
