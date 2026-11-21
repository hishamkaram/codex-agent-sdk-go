package threadexec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type approvedPlanState uint8

const (
	approvedPlanIdle approvedPlanState = iota
	approvedPlanPreparing
	approvedPlanUnconfirmed
)

// Prepare holds an execution reservation through the matching response.
// validate confirms the response before the destination can accept work.
func (e *Record) Prepare(ctx context.Context, id, method string, params any, wire Wire, validate func(json.RawMessage) error) (jsonrpc.Response, error) {
	if err := e.reserveApprovedPlan(ctx, id, wire); err != nil {
		return jsonrpc.Response{}, err
	}
	next := approvedPlanIdle
	defer func() { e.finishApprovedPlan(next) }()

	pending, err := wire.PostGuarded(ctx, method, params, func(ctx context.Context) error {
		return e.checkApprovedPlanDispatch(ctx, id, wire)
	})
	if err != nil {
		return jsonrpc.Response{}, err
	}
	defer pending.Release()
	response, err := pending.Wait(ctx)
	if err != nil {
		next = approvedPlanUnconfirmed
		return jsonrpc.Response{}, fmt.Errorf("preparation outcome is unknown: %w", err)
	}
	if response.Error == nil {
		if err := validate(response.Result); err != nil {
			next = approvedPlanUnconfirmed
			return jsonrpc.Response{}, err
		}
	}
	return response, nil
}

func (e *Record) reserveApprovedPlan(ctx context.Context, id string, wire Wire) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed.Load() {
		return types.ErrThreadClosed
	}
	if e.approvedPlan != approvedPlanIdle || e.op != nil || e.externalActive || e.resuming != 0 || e.ActiveID() != "" {
		return wire.Unconfirmed(id)
	}
	e.approvedPlan = approvedPlanPreparing
	e.signalLocked()
	return nil
}

func (e *Record) checkApprovedPlanDispatch(ctx context.Context, id string, wire Wire) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkDispatchLocked(ctx, id, e.approvedPlan == approvedPlanPreparing && e.op == nil, wire)
}

func (e *Record) finishApprovedPlan(next approvedPlanState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.approvedPlan = next
	e.signalLocked()
}
