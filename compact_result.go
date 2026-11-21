package codex

import (
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// CompactResult is returned by Thread.Compact. The `thread/compact/start`
// RPC is async — codex acknowledges the request immediately but the
// actual compaction happens in the background and is signaled by a
// `thread/compacted` notification.
//
// The hybrid pattern: callers can fire-and-forget (just let the result
// be GC'd) OR block for completion via Wait. Mirrors the
// `exec.Cmd.Start()` + `exec.Cmd.Wait()` pattern.
//
// Internally, CompactResult wraps a one-shot channel the Thread's
// dispatcher fills when the `*types.ContextCompacted` event arrives.
// The subscription is installed BEFORE the RPC is sent so the
// notification can never arrive before the receiver is ready.
type CompactResult struct {
	// ThreadID is the thread the compaction was requested on.
	ThreadID string

	waiter threadexec.CompactWaiter
}

// Wait blocks until the matching `thread/compacted` notification
// arrives, ctx is canceled, or the Thread closes. Returns the parsed
// event or the ctx error.
//
// Wait is idempotent: subsequent calls return the same event (the
// subscription is drained on the first call and cached).
func (r *CompactResult) Wait(ctx context.Context) (*types.ContextCompacted, error) {
	if r == nil {
		return nil, fmt.Errorf("codex.CompactResult.Wait: nil result")
	}
	return r.waiter.Wait(ctx)
}
