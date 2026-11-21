package codex

import (
	"context"
	"errors"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

var errThreadEventIdentityMissing = errors.New("thread-scoped notification is missing required thread ID")

// dispatch runs the event-routing goroutine. Reads notifications and
// server-initiated requests from the demux and fans them out.
func (c *Client) dispatch(ctx context.Context, demux *jsonrpc.Demux, done chan struct{}) {
	workerCtx, cancel := context.WithCancel(ctx)
	jobs := make(chan approvalJob, approvalQueueSize)
	workerDone := make(chan struct{})
	go c.runApprovalWorker(workerCtx, demux, jobs, workerDone)
	defer func() {
		cancel()
		c.connected.Store(false)
		c.closeThreadEventSubscriptions()
		c.clearFileApprovals()
		c.closeThreadExecutions()
		<-workerDone
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
			c.routeServerMessage(workerCtx, demux, jobs, message)
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
			c.enqueueApproval(workerCtx, demux, jobs, sreq)
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

func (c *Client) routeServerMessage(ctx context.Context, demux *jsonrpc.Demux, jobs chan<- approvalJob, message jsonrpc.ServerMessage) {
	if message.Notification != nil {
		c.handleNotification(*message.Notification)
	} else if message.Request != nil {
		c.enqueueApproval(ctx, demux, jobs, *message.Request)
	}
}

func (c *Client) handleNotification(n jsonrpc.Notification) {
	if n.DecodeError != nil {
		c.invalidateThreadExecutions("")
		c.clearFileApprovals()
		c.failThreadEventSubscriptions(ThreadEventEnvelope{
			Err: &ThreadEventFrameGapError{Cause: n.DecodeError},
		})
		c.logger.Warn("decode inbound frame failed", zap.Error(n.DecodeError))
		return
	}

	ev, err := events.ParseEvent(n)
	if err != nil {
		c.clearFileApprovals()
		threadID, _ := extractThreadID(n.Params)
		c.invalidateThreadExecutions(threadID)
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
			c.invalidateThreadExecutions("")
			c.clearFileApprovals()
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
	c.rememberFileApproval(ev)
	c.mu.Lock()
	execution := c.executions[threadID]
	c.mu.Unlock()
	if execution != nil {
		// Record lifecycle evidence before any lossy event delivery.
		execution.observe(ev)
	}
	// Unregistered provider-created child threads have no Thread handle, but
	// their events still belong in the client-wide runtime stream.
	c.publishThreadEvent(ThreadEventEnvelope{ThreadID: threadID, Event: ev})
}

func (c *Client) invalidateThreadExecutions(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if threadID != "" {
		if execution := c.executions[threadID]; execution != nil {
			execution.invalidate()
		}
		return
	}
	for _, execution := range c.executions {
		execution.invalidate()
	}
}

func (c *Client) closeThreadExecutions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, execution := range c.executions {
		execution.close()
	}
}

// registerThread stores a Thread in the client's routing table. The caller
// must own the thread's ID (i.e., thread/start or thread/resume succeeded).
// Also records the thread ID as the latest for SessionID().
func (c *Client) registerThread(t *Thread) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads == nil {
		t.markClosed()
		return
	}
	if c.executions == nil {
		c.executions = make(map[string]*threadExecution)
	}
	if existing := c.executions[t.id]; existing != nil {
		t.threadExecution = existing
	} else {
		if t.threadExecution == nil {
			t.threadExecution = newThreadExecution()
		}
		if !c.connected.Load() {
			t.markClosed()
		}
		c.executions[t.id] = t.threadExecution
	}
	c.threads[t.id] = t
	c.latestThreadID = t.id
}

// unregisterThread removes a Thread from routing. Called when the thread
// is archived or the caller drops it. Clears latestThreadID when the
// removed thread was the most recent so SessionID() reverts to "" for
// single-thread callers.
func (c *Client) unregisterThread(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.threads, threadID)
	if execution := c.executions[threadID]; execution != nil {
		// Keep unresolved activity for handles that still refer to this ID.
		execution.invalidate()
	}
	if c.latestThreadID == threadID {
		c.latestThreadID = ""
	}
}
