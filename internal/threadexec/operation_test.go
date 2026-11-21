package threadexec

import (
	"context"
	"errors"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
)

type cancelAfterContextCheck struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelAfterContextCheck) Err() error {
	err := c.Context.Err()
	// Make cancellation land after the guard's first observation.
	c.cancel()
	return err
}

func TestThread_DispatchRechecksCancellationBeforeCommit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	demux := jsonrpc.NewDemux(nil, nil, nil, jsonrpc.WithOrderedServerMessages())
	record := NewRecord(false)
	wire := Wire{PendingActivity: func() (bool, error) {
		return demux.CheckPendingNotification(func(jsonrpc.Notification) bool { return false })
	}}
	op, err := record.admit(ctx, "T", Turn, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = record.markDispatch(cancelAfterContextCheck{Context: ctx, cancel: cancel}, op, wire)
	if !errors.Is(err, context.Canceled) || op.dispatched {
		t.Fatalf("dispatch error = %v, dispatched = %t, want canceled before commit", err, op.dispatched)
	}
}
