package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ErrThreadExecutionUnconfirmed means another provider operation may still
// own this thread. The SDK sends no new work until it sees matching completion
// or proves no operation started. Close the client if the provider stays silent.
var ErrThreadExecutionUnconfirmed = errors.New("codex: thread execution is unconfirmed")

// ThreadExecutionUnconfirmedError identifies the affected provider thread.
type ThreadExecutionUnconfirmedError struct {
	ThreadID string
}

func (e *ThreadExecutionUnconfirmedError) Error() string {
	return fmt.Sprintf("%v for thread %q", ErrThreadExecutionUnconfirmed, e.ThreadID)
}

func (*ThreadExecutionUnconfirmedError) Unwrap() error { return ErrThreadExecutionUnconfirmed }

const executionReconciliationLimit = 5 * time.Second

type executionKind uint8

const (
	executeTurn executionKind = iota
	executeReview
	executeCompact
)

// threadExecution is the Client's authoritative state for one provider thread
// ID. The ordered dispatcher records evidence before publishing any event.
// The other writers are the reserved operation's request and cleanup owners.
type threadExecution struct {
	mu      sync.Mutex
	changed chan struct{}
	op      *threadOperation

	// Private threads were freshly created on this Client's stdio connection.
	// An unclassified start, gap or archive permanently disqualifies ownership.
	private        bool
	externalActive bool
	lastTerminalID string
	baseline       usageBaseline

	activeTurnID atomic.Value // string, a snapshot for manual Steer/Interrupt
	closed       atomic.Bool
	done         chan struct{}
	compactSub   atomic.Pointer[chan *types.ContextCompacted]
}

type threadOperation struct {
	kind      executionKind
	events    chan types.ThreadEvent
	consuming bool
	cleaning  bool
	uncertain bool
	waitUntil time.Time

	acknowledged   bool
	ackID          string
	rootStartID    string
	terminalID     string
	terminal       types.ThreadEvent
	earlyTerminals map[string]types.ThreadEvent
	compactID      string
	compactCh      *chan *types.ContextCompacted
	compactDone    bool
	complete       bool

	usage          turnUsageLedger
	finalUsage     types.TokenUsage
	usageAvailable bool
}

func newThreadExecution() *threadExecution {
	e := &threadExecution{changed: make(chan struct{}), done: make(chan struct{})}
	e.activeTurnID.Store("")
	return e
}

func (e *threadExecution) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed.CompareAndSwap(false, true) {
		return
	}
	close(e.done)
	if sub := e.compactSub.Swap(nil); sub != nil {
		close(*sub)
	}
}

func (e *threadExecution) signalLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
}

func (e *threadExecution) invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.private = false
	e.baseline = usageBaseline{}
	if e.op != nil {
		e.op.usage.broken = true
	}
	e.signalLocked()
}

func (e *threadExecution) admit(ctx context.Context, threadID string, kind executionKind, streamed bool) (*threadOperation, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e.mu.Lock()
		if e.closed.Load() {
			e.mu.Unlock()
			return nil, types.ErrThreadClosed
		}
		op := e.op
		if e.externalActive || op != nil && (op.uncertain || (!op.waitUntil.IsZero() && !time.Now().Before(op.waitUntil))) {
			e.mu.Unlock()
			return nil, &ThreadExecutionUnconfirmedError{ThreadID: threadID}
		}
		if op == nil {
			reserved, reserveErr := e.reserveLocked(kind, streamed)
			e.mu.Unlock()
			return reserved, reserveErr
		}
		change, waitUntil := e.changed, op.waitUntil
		e.mu.Unlock()
		if err := e.waitForAdmission(ctx, change, waitUntil); err != nil {
			return nil, err
		}
	}
}

func (e *threadExecution) reserveLocked(kind executionKind, streamed bool) (*threadOperation, error) {
	op := &threadOperation{kind: kind, consuming: streamed, usage: newTurnUsageLedger(e.baseline)}
	if streamed {
		op.events = make(chan types.ThreadEvent, ThreadInboxBuffer)
	}
	if kind == executeCompact {
		ch := make(chan *types.ContextCompacted, 1)
		if !e.compactSub.CompareAndSwap(nil, &ch) {
			return nil, errors.New("codex: a compaction result is already pending")
		}
		op.compactCh = &ch
	}
	e.op = op
	return op, nil
}

func (e *threadExecution) waitForAdmission(ctx context.Context, change <-chan struct{}, waitUntil time.Time) error {
	var alarm <-chan time.Time
	if !waitUntil.IsZero() {
		timer := time.NewTimer(time.Until(waitUntil))
		defer timer.Stop()
		alarm = timer.C
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return types.ErrThreadClosed
	case <-change:
	case <-alarm:
	}
	return nil
}

// reject clears a reservation only if the provider or writer rejected the
// operation without any evidence of provider work.
func (e *threadExecution) reject(op *threadOperation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op.consuming = false
	if op.compactCh != nil {
		e.compactSub.CompareAndSwap(op.compactCh, nil)
	}
	if op.rootStartID == "" && op.terminalID == "" && op.compactID == "" && !op.compactDone {
		op.complete = true
	} else {
		op.uncertain = true
	}
	e.releaseLocked(op)
	e.signalLocked()
}

func (e *threadExecution) acknowledge(op *threadOperation, result json.RawMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var reply struct {
		Turn struct {
			ID         string `json:"id"`
			RootTurnID string `json:"rootTurnId"`
		} `json:"turn"`
	}
	if op.kind != executeCompact {
		_ = json.Unmarshal(result, &reply)
	}
	op.ackID = reply.Turn.ID
	op.acknowledged = true
	if op.ackID != "" && !isRootTurn(op.ackID, reply.Turn.RootTurnID) {
		e.private = false
		op.usage.broken = true
	}
	if op.kind == executeCompact && op.waitUntil.IsZero() {
		op.waitUntil = time.Now().Add(executionReconciliationLimit)
	}
	if op.ackID != "" && op.rootStartID != "" && op.ackID != op.rootStartID {
		e.externalActive = e.activeID() == op.rootStartID
		e.private = false
		op.usage.broken = true
	}
	if op.ackID != "" && op.rootStartID == "" && op.terminalID != op.ackID && op.earlyTerminals[op.ackID] == nil && e.activeID() == "" {
		e.activeTurnID.Store(op.ackID)
	}
	e.finishLocked(op)
	e.signalLocked()
}

func (t *Thread) isClosed() bool { return t.threadExecution != nil && t.closed.Load() }

// seedExternalActivity is used before publishing a resumed or forked
// handle. An active status without a unique ID never permits terminal
// notifications for arbitrary turns to release admission.
func (e *threadExecution) seedExternalActivity(result json.RawMessage) {
	var reply struct {
		Thread struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
			Turns []struct {
				ID         string `json:"id"`
				RootTurnID string `json:"rootTurnId"`
				Status     string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &reply) != nil || reply.Thread.Status.Type != "active" {
		return
	}
	e.externalActive = true
	active := ""
	for _, turn := range reply.Thread.Turns {
		if turn.Status == "inProgress" && isRootTurn(turn.ID, turn.RootTurnID) {
			if active != "" {
				return
			}
			active = turn.ID
		}
	}
	e.activeTurnID.Store(active)
}

func isRootTurn(id, rootID string) bool {
	return id != "" && (rootID == "" || rootID == id)
}

func (e *threadExecution) activeID() string {
	id, _ := e.activeTurnID.Load().(string)
	return id
}

func (e *threadExecution) consumeDone(op *threadOperation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op.consuming = false
	e.releaseLocked(op)
	e.signalLocked()
}

func (e *threadExecution) releaseLocked(op *threadOperation) {
	if e.op == op && op.complete && !op.consuming && !op.cleaning {
		e.op = nil
	}
}

func (e *threadExecution) observe(ev types.ThreadEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.Load() {
		return
	}
	op := e.op
	switch event := ev.(type) {
	case *types.TurnStarted:
		e.observeStartLocked(op, event)
	case *types.TurnCompleted:
		e.observeTerminalLocked(op, event.TurnID, event)
		return
	case *types.TurnFailed:
		e.observeTerminalLocked(op, event.TurnID, event)
		return
	case *types.TokenUsageUpdated:
		e.observeUsageLocked(op, event)
	case *types.ItemCompleted:
		if _, ok := event.Item.(*types.ContextCompaction); ok {
			e.baseline = usageBaseline{}
			if op != nil && !op.complete {
				op.usage.broken = true
				if op.kind == executeCompact && (op.rootStartID == "" || event.TurnID == op.rootStartID) {
					op.compactID = event.TurnID
					e.finishLocked(op)
				}
			}
		}
	case *types.ContextCompacted:
		e.observeLegacyCompactLocked(op, event)
	}
	e.queueEventLocked(op, ev)
}

func (e *threadExecution) observeStartLocked(op *threadOperation, event *types.TurnStarted) {
	id := event.TurnID
	if !isRootTurn(id, event.RootTurnID) || id == e.lastTerminalID {
		return
	}
	e.activeTurnID.Store(id)
	if op == nil || op.complete || (op.rootStartID != "" && op.rootStartID != id) {
		e.externalActive = true
		e.private = false
		e.baseline = usageBaseline{}
		if op != nil && !op.complete {
			op.usage.broken = true
		}
	} else {
		op.rootStartID = id
		if terminal := op.earlyTerminals[id]; terminal != nil {
			op.setTerminal(id, terminal)
			e.activeTurnID.Store("")
			e.finishLocked(op)
		}
		if op.ackID != "" && op.ackID != id {
			e.externalActive = true
			e.private = false
			op.usage.broken = true
		}
	}
	e.signalLocked()
}

func (e *threadExecution) observeTerminalLocked(op *threadOperation, id string, ev types.ThreadEvent) {
	if id == "" || id == e.lastTerminalID {
		return
	}
	if e.activeID() == id {
		e.activeTurnID.Store("")
		e.externalActive = false
	}
	if op == nil || op.complete {
		e.lastTerminalID = id
		e.signalLocked()
		return
	}

	expected := op.expectedID()
	if expected == id {
		op.setTerminal(id, ev)
	} else if !op.acknowledged && len(op.earlyTerminals) < ThreadInboxBuffer {
		// Several stale terminal IDs can precede the acknowledgement. Retain
		// bounded candidates, then select by the actual reply or fresh root ID.
		if op.earlyTerminals == nil {
			op.earlyTerminals = make(map[string]types.ThreadEvent)
		}
		op.earlyTerminals[id] = copyTerminal(ev)
	}
	e.finishLocked(op)
	e.signalLocked()
}

func (op *threadOperation) expectedID() string {
	if op.ackID != "" {
		return op.ackID
	}
	if op.rootStartID != "" {
		return op.rootStartID
	}
	return op.compactID
}

func copyTerminal(ev types.ThreadEvent) types.ThreadEvent {
	switch event := ev.(type) {
	case *types.TurnCompleted:
		snapshot := *event
		return &snapshot
	case *types.TurnFailed:
		snapshot := *event
		return &snapshot
	default:
		return ev
	}
}

func (op *threadOperation) setTerminal(id string, ev types.ThreadEvent) {
	op.terminalID = id
	op.terminal = copyTerminal(ev)
}

func (e *threadExecution) observeUsageLocked(op *threadOperation, event *types.TokenUsageUpdated) {
	if op == nil || op.complete || event.TurnID == "" || event.TurnID != op.rootStartID {
		e.baseline = usageBaseline{}
		if op != nil && !op.complete {
			op.usage.broken = true
		}
		return
	}
	if op.terminalID != "" || op.earlyTerminals[event.TurnID] != nil {
		op.usage.broken = true
		e.baseline = usageBaseline{}
		return
	}
	op.usage.record(event)
}

func (e *threadExecution) observeLegacyCompactLocked(op *threadOperation, event *types.ContextCompacted) {
	if sub := e.compactSub.Load(); sub != nil {
		select {
		case *sub <- event:
		default:
		}
	}
	if op != nil && !op.complete && op.kind == executeCompact && e.private &&
		event.TurnID != "" && event.TurnID != e.lastTerminalID &&
		(op.rootStartID == "" || event.TurnID == op.rootStartID) {
		e.baseline = usageBaseline{}
		op.usage.broken = true
		op.compactDone = true
		e.finishLocked(op)
		e.signalLocked()
	}
}

func (e *threadExecution) queueEventLocked(op *threadOperation, ev types.ThreadEvent) {
	if op == nil || op.complete || !op.consuming {
		return
	}
	select {
	case op.events <- ev:
	default:
		// Terminal state is retained separately even when this queue is full.
	}
}

func (e *threadExecution) finishLocked(op *threadOperation) {
	// A matching terminal can resolve an exclusive start even if the
	// acknowledgement remained silent through the cleanup budget.
	if op.complete || !op.acknowledged && !e.exclusiveTerminalEvidence(op) {
		return
	}
	expected := op.expectedID()
	if terminal := op.earlyTerminals[expected]; terminal != nil && op.terminalID != expected {
		op.setTerminal(expected, terminal)
	}
	hasTerminus := expected != "" && op.terminalID == expected
	if !hasTerminus && !e.confirmedLegacyCompact(op) {
		return
	}
	op.complete = true
	op.earlyTerminals = nil
	if hasTerminus {
		e.lastTerminalID = expected
	}
	op.finalUsage, op.usageAvailable = op.usage.final()
	if terminal, ok := op.terminal.(*types.TurnCompleted); ok && terminal.UsagePresent {
		op.finalUsage, op.usageAvailable = terminal.Usage, true
	}
	e.baseline = op.usage.nextBaseline()
	if op.events != nil {
		close(op.events)
	}
	e.releaseLocked(op)
}

func (e *threadExecution) exclusiveTerminalEvidence(op *threadOperation) bool {
	return e.private && op.uncertain && op.rootStartID != ""
}

func (e *threadExecution) confirmedLegacyCompact(op *threadOperation) bool {
	return op.kind == executeCompact && op.compactDone && e.activeID() == ""
}

func (t *Thread) beginOperation(ctx context.Context, kind executionKind, streamed bool, method string, params any) (*threadOperation, jsonrpc.Response, error) {
	if t.isClosed() || t.threadExecution == nil {
		return nil, jsonrpc.Response{}, types.ErrThreadClosed
	}
	if err := t.client.checkApprovalReentry(ctx); err != nil {
		return nil, jsonrpc.Response{}, err
	}
	op, err := t.admit(ctx, t.id, kind, streamed)
	if err != nil {
		return nil, jsonrpc.Response{}, err
	}
	pending, err := t.client.demux.Post(ctx, method, params)
	if err != nil {
		t.reject(op)
		return nil, jsonrpc.Response{}, err
	}
	resp, err := pending.Wait(ctx)
	if err != nil {
		t.startExecutionCleanup(ctx, op, pending)
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

func (t *Thread) startExecutionCleanup(ctx context.Context, op *threadOperation, pending *jsonrpc.PendingResponse) {
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
	go t.reconcileExecution(context.WithoutCancel(ctx), op, pending)
}

func (t *Thread) reconcileExecution(parent context.Context, op *threadOperation, pending *jsonrpc.PendingResponse) {
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
	t.awaitExecutionTerminal(ctx, op)
}

func (t *Thread) awaitExecutionTerminal(ctx context.Context, op *threadOperation) {
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
			if err := t.client.InterruptThreadTurn(ctx, t.id, interruptID); err != nil && ctx.Err() == nil {
				t.client.logger.Warn("turn interrupt was not confirmed")
			}
		}
		if err := t.waitForAdmission(ctx, changed, time.Time{}); err != nil {
			return
		}
	}
}

func (e *threadExecution) ownedInterruptTargetLocked(op *threadOperation) string {
	if op.kind == executeCompact || !e.private || e.externalActive {
		return ""
	}
	if op.ackID == op.rootStartID && op.ackID == e.activeID() {
		return op.ackID
	}
	return ""
}
