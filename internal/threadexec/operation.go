package threadexec

import (
	"context"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Wire is the operation's access to the serialized session and its diagnostics.
// PendingActivity and Unconfirmed are called in the writer guard. They must
// not call user code or acquire another execution or registry lock.
type Wire struct {
	PostGuarded     func(context.Context, string, any, func(context.Context) error) (*jsonrpc.PendingResponse, error)
	PendingActivity func() (bool, error)
	Interrupt       func(context.Context, string, string) error
	Unconfirmed     func(string) error
	Warn            func(string)
}

func (t *Record) Begin(ctx context.Context, threadID string, kind Kind, streamed bool, method string, params any, wire Wire) (*Operation, jsonrpc.Response, error) {
	op, err := t.admit(ctx, threadID, kind, streamed, wire.Unconfirmed)
	if err != nil {
		return nil, jsonrpc.Response{}, err
	}
	pending, err := wire.PostGuarded(ctx, method, params, func(writeCtx context.Context) error {
		return t.markDispatch(writeCtx, op, wire)
	})
	if err != nil {
		t.reject(op)
		return nil, jsonrpc.Response{}, err
	}
	resp, err := pending.Wait(ctx)
	if err != nil {
		t.startExecutionCleanup(ctx, op, pending, wire)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, jsonrpc.Response{}, ctxErr
		}
		return nil, jsonrpc.Response{}, err
	}
	pending.Release()
	if resp.Error != nil {
		t.reject(op)
		return nil, resp, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	t.acknowledge(op, resp.Result)
	return op, resp, nil
}

func (t *Record) markDispatch(ctx context.Context, op *Operation, wire Wire) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkDispatchLocked(ctx, op.wireID, t.op == op, wire); err != nil {
		return err
	}
	op.dispatched = true
	return nil
}

// checkDispatchLocked checks the provider activity visible at the writer
// boundary. The caller holds the execution lock and owns the reservation.
func (t *Record) checkDispatchLocked(ctx context.Context, threadID string, ownsReservation bool, wire Wire) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.closed.Load() {
		return types.ErrThreadClosed
	}
	if !ownsReservation || t.externalActive || t.resuming != 0 || t.ActiveID() != "" {
		return wire.Unconfirmed(threadID)
	}
	pending, err := wire.PendingActivity()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	if pending {
		return wire.Unconfirmed(threadID)
	}
	return nil
}

func (t *Record) startExecutionCleanup(ctx context.Context, op *Operation, pending *jsonrpc.PendingResponse, wire Wire) {
	t.mu.Lock()
	op.consuming = false
	if pending != nil && op.compactCh != nil {
		t.compactSub.CompareAndSwap(op.compactCh, nil)
	}
	if op.complete || t.closed.Load() {
		t.releaseLocked(op)
		t.signalLocked()
		t.mu.Unlock()
		if pending != nil {
			pending.Release()
		}
		return
	}
	op.cleaning = true
	op.waitUntil = time.Now().Add(executionReconciliationLimit)
	t.signalLocked()
	t.mu.Unlock()
	go t.reconcileExecution(context.WithoutCancel(ctx), op, pending, wire)
}

func (t *Record) reconcileExecution(parent context.Context, op *Operation, pending *jsonrpc.PendingResponse, wire Wire) {
	t.mu.Lock()
	deadline := op.waitUntil
	t.mu.Unlock()
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	defer func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		op.cleaning = false
		if !op.complete && !t.closed.Load() {
			op.uncertain = true
			t.finishLocked(op)
			if !op.complete {
				wire.Warn("provider execution remains unconfirmed after cancellation")
			}
		}
		t.releaseLocked(op)
		t.signalLocked()
	}()
	if pending != nil {
		defer pending.Release()
		reply, err := pending.Wait(ctx)
		if err != nil {
			return
		}
		if reply.Error != nil {
			t.reject(op)
			return
		}
		t.acknowledge(op, reply.Result)
	}
	t.awaitExecutionTerminal(ctx, op, wire)
}

func (t *Record) awaitExecutionTerminal(ctx context.Context, op *Operation, wire Wire) {
	interrupted := false
	for {
		t.mu.Lock()
		if t.closed.Load() || op.complete {
			t.mu.Unlock()
			return
		}
		// turn/start responses also describe steering. A reply alone is not
		// proof that this operation started the turn it refers to.
		interruptID := ""
		if !interrupted {
			interruptID = t.ownedInterruptTargetLocked(op)
		}
		changed := t.changed
		t.mu.Unlock()
		if interruptID != "" {
			interrupted = true
			if err := wire.Interrupt(ctx, op.wireID, interruptID); err != nil && ctx.Err() == nil {
				wire.Warn("turn interrupt was not confirmed")
			}
		}
		if err := t.waitForAdmission(ctx, changed, time.Time{}); err != nil {
			return
		}
	}
}

func (e *Record) ownedInterruptTargetLocked(op *Operation) string {
	if op.kind == Compact || !e.private || e.externalActive {
		return ""
	}
	if op.ackID == op.rootStartID && op.ackID == e.ActiveID() {
		return op.ackID
	}
	return ""
}
