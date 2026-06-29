package codex

import (
	"context"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// dispatch runs the event-routing goroutine. Reads notifications and
// server-initiated requests from the demux and fans them out.
func (c *Client) dispatch(ctx context.Context, demux *jsonrpc.Demux, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case note, ok := <-demux.Notifications():
			if !ok {
				return
			}
			c.handleNotification(note)
		case sreq, ok := <-demux.ServerRequests():
			if !ok {
				return
			}
			c.handleServerRequest(ctx, demux, sreq)
		}
	}
}

func (c *Client) handleNotification(n jsonrpc.Notification) {
	ev, err := events.ParseEvent(n)
	if err != nil {
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
	threadID := extractThreadIDFromEvent(ev)
	if threadID == "" {
		// Global events — configWarning, account/rateLimits/updated, etc.
		// Logged at debug; clients that want them must expose a hook in v1.1.
		c.logger.Debug("unroutable event (no thread_id)",
			zap.String("method", ev.EventMethod()))
		return
	}
	c.mu.Lock()
	t := c.threads[threadID]
	c.mu.Unlock()
	if t == nil {
		// Thread may not be registered yet (event arrived before
		// StartThread stored the Thread). Ignore — the spike transcript
		// showed mcpServer/startupStatus/updated arriving before thread/
		// started which we don't route anyway.
		return
	}
	t.deliverEvent(ev)
}

func (c *Client) handleServerRequest(ctx context.Context, demux *jsonrpc.Demux, sreq jsonrpc.ServerRequest) {
	req, err := events.ParseApprovalRequest(sreq.Method, sreq.Params)
	if err != nil {
		c.logger.Warn("parse server-request failed",
			zap.String("method", sreq.Method),
			zap.Error(err))
		_ = demux.RespondServerRequest(sreq.ID, nil, &jsonrpc.RPCError{
			Code:    -32000,
			Message: "client parse error: " + err.Error(),
		})
		return
	}
	cb := c.opts.ApprovalCallback
	if cb == nil {
		cb = types.DefaultDenyApprovalCallback
	}
	// Use dispatcherCtx so callbacks get canceled on Close.
	decision := cb(ctx, req)
	result := events.EncodeApprovalDecision(decision)
	if err := demux.RespondServerRequest(sreq.ID, result, nil); err != nil {
		c.logger.Warn("approval response write failed",
			zap.String("method", sreq.Method),
			zap.Error(err))
	}
}

// registerThread stores a Thread in the client's routing table. The caller
// must own the thread's ID (i.e., thread/start or thread/resume succeeded).
// Also records the thread ID as the latest for SessionID().
func (c *Client) registerThread(t *Thread) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads != nil {
		c.threads[t.id] = t
		c.latestThreadID = t.id
	}
}

// unregisterThread removes a Thread from routing. Called when the thread
// is archived or the caller drops it. Clears latestThreadID when the
// removed thread was the most recent so SessionID() reverts to "" for
// single-thread callers.
func (c *Client) unregisterThread(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads != nil {
		delete(c.threads, threadID)
	}
	if c.latestThreadID == threadID {
		c.latestThreadID = ""
	}
}
