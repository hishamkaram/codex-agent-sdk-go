package threadexec

import "context"

// ReserveSnapshot keeps same-client execution idle until release. Use it
// across the request that snapshots provider history.
func (e *Record) ReserveSnapshot(ctx context.Context, threadID string, unconfirmed func(string) error) (release func(), err error) {
	op, err := e.admit(ctx, threadID, historyAccess, false, unconfirmed)
	if err != nil {
		return nil, err
	}
	return func() { e.finishHistoryAccess(op, false, false) }, nil
}

func (e *Record) finishHistoryAccess(op *Operation, mutated, uncertain bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if mutated {
		e.invalidateUsageLocked()
	}
	op.uncertain = uncertain
	op.complete = !uncertain
	e.releaseLocked(op)
	e.signalLocked()
}
