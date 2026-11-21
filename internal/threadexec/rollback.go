package threadexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadhistory"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Rollback owns one history mutation across its legacy request, any fallback
// lookup, and the revert acknowledgement. Notifications still describe
// external activity because this operation does not start a provider turn.
func (e *Record) Rollback(ctx context.Context, id string, count int, wire Wire, read threadhistory.Send) error {
	op, err := e.admit(ctx, id, historyAccess, false, wire.Unconfirmed)
	if err != nil {
		return err
	}
	e.mu.Lock()
	generation := e.activeGeneration
	e.mu.Unlock()

	var sent, uncertain bool
	defer func() { e.finishHistoryAccess(op, sent, uncertain) }()

	post := func(method string, params any) (jsonrpc.Response, error) {
		pending, postErr := wire.PostGuarded(ctx, method, params, func(writeCtx context.Context) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.checkDispatchLocked(writeCtx, id, e.op == op && e.activeGeneration == generation, wire)
		})
		if postErr != nil {
			return jsonrpc.Response{}, postErr
		}
		sent = true
		defer pending.Release()
		reply, waitErr := pending.Wait(ctx)
		if waitErr != nil {
			uncertain = true
			return jsonrpc.Response{}, fmt.Errorf("%s outcome is unconfirmed: %w", method, waitErr)
		}
		return reply, nil
	}

	legacy, err := post("thread/rollback", map[string]any{"threadId": id, "numTurns": count})
	if err != nil {
		return err
	}
	if !rollbackMethodRejected("thread/rollback", legacy.Error) {
		if legacy.Error != nil {
			return types.NewRPCError(legacy.Error.Code, legacy.Error.Message, legacy.Error.Data)
		}
		return nil
	}
	// The decoder rejected the legacy request before any history mutation.
	sent = false
	before, err := threadhistory.RollbackBoundary(ctx, read, id, count)
	if err != nil {
		return err
	}
	reverted, err := post("thread/revert", map[string]string{"threadId": id, "beforeTurnId": before})
	if err != nil {
		return err
	}
	if reverted.Error != nil {
		// The provider mutates persisted history before reloading its thread
		// runtime. An error from that reload does not undo the mutation.
		rpcErr := types.NewRPCError(reverted.Error.Code, reverted.Error.Message, reverted.Error.Data)
		uncertain = !revertDefinitelyRejected(reverted.Error)
		if uncertain {
			return errors.Join(rpcErr, wire.Unconfirmed(id))
		}
		return rpcErr
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(reverted.Result, &result); err != nil {
		uncertain = true
		return fmt.Errorf("decode thread/revert result: %w", err)
	}
	if result.Thread.ID == "" || CanonicalID(result.Thread.ID) != CanonicalID(id) {
		uncertain = true
		return errors.New("thread/revert result does not identify the requested thread")
	}
	return nil
}

// The two cases are definitive pre-dispatch rejections. A broad
// Invalid request, cancellation, or unexpected reply cannot prove that.
func rollbackMethodRejected(method string, err *jsonrpc.RPCError) bool {
	unknownVariant := "Invalid request: unknown variant `" + method + "`"
	return err != nil && (err.Code == -32601 || (err.Code == -32600 &&
		(err.Message == unknownVariant || strings.HasPrefix(err.Message, unknownVariant+", expected "))))
}

func revertDefinitelyRejected(err *jsonrpc.RPCError) bool {
	if rollbackMethodRejected("thread/revert", err) {
		return true
	}
	// The runtime rejects these unsupported storage modes before shutdown
	// or mutation. Preserve the running thread after those RPC errors.
	return err.Code == -32600 &&
		(err.Message == "ephemeral threads do not support thread/revert" ||
			err.Message == "thread/revert only supports paginated threads")
}
