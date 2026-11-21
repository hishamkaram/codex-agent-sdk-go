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
	mu                  sync.Mutex
	changed             chan struct{}
	op                  *threadOperation
	resuming            int  // guarded by mu; new work waits for the active-status reply
	pendingRegistration bool // guarded by Client.mu until registration

	// Private threads were freshly created on this Client's stdio connection.
	// An unclassified start, gap or archive permanently disqualifies ownership.
	private          bool
	externalActive   bool
	activeGeneration uint64
	// A resume snapshot can identify a terminal seen after the current
	// unknown-active fence. A later active fence discards that evidence.
	fencedTerminals map[string]struct{}
	lastTerminalID  string
	baseline        usageBaseline

	activeTurnID  atomic.Value // string, a snapshot for manual Steer/Interrupt
	closed        atomic.Bool
	done          chan struct{}
	compactSub    atomic.Pointer[chan *types.ContextCompacted]
	compactTurnID string // guarded by mu; retained for the matched legacy waiter
}

type threadOperation struct {
	kind      executionKind
	events    chan types.ThreadEvent
	consuming bool
	cleaning  bool
	uncertain bool
	waitUntil time.Time

	dispatched     bool
	acknowledged   bool
	ackID          string
	rootStartID    string
	terminalID     string
	terminal       types.ThreadEvent
	earlyTerminals map[string]types.ThreadEvent
	compactID      string
	compactCh      *chan *types.ContextCompacted
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
	// A gap could hide new activity, so an in-flight idle reply cannot
	// reconcile an older external fence across the gap.
	e.activeGeneration++
	e.invalidateUsageLocked()
	e.signalLocked()
}

func (e *threadExecution) invalidateUsageLocked() {
	e.baseline = usageBaseline{}
	if e.op != nil && !e.op.complete {
		e.op.usage.broken = true
	}
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
		if e.externalActive || op != nil && !op.complete && (op.uncertain || (!op.waitUntil.IsZero() && !time.Now().Before(op.waitUntil))) {
			e.mu.Unlock()
			return nil, &ThreadExecutionUnconfirmedError{ThreadID: threadID}
		}
		if op == nil && e.resuming == 0 {
			reserved, reserveErr := e.reserveLocked(kind, streamed)
			e.mu.Unlock()
			return reserved, reserveErr
		}
		change := e.changed
		var waitUntil time.Time
		if op != nil && !op.complete {
			waitUntil = op.waitUntil
		}
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
		e.compactTurnID = ""
	}
	e.op = op
	e.signalLocked()
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
	if op.rootStartID == "" && op.terminalID == "" && op.compactID == "" {
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

type providerThreadStatus struct {
	Type string `json:"type"`
}

// seedExternalActivity is used before publishing a resumed or forked
// handle. An active status without a unique ID never permits terminal
// notifications for arbitrary turns to release admission.
func (e *threadExecution) seedExternalActivity(result json.RawMessage) string {
	var reply struct {
		Thread struct {
			Status providerThreadStatus `json:"status"`
			Turns  []struct {
				ID         string `json:"id"`
				RootTurnID string `json:"rootTurnId"`
				Status     string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &reply) != nil {
		return ""
	}
	status := reply.Thread.Status.Type
	if status != "active" {
		return status
	}
	e.externalActive = true
	active := ""
	for _, turn := range reply.Thread.Turns {
		if turn.Status == "inProgress" && isRootTurn(turn.ID, turn.RootTurnID) {
			if active != "" {
				return status
			}
			active = turn.ID
		}
	}
	e.activeTurnID.Store(active)
	return status
}

func (e *threadExecution) resumeIdleCandidate() (generation uint64, eligible bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeGeneration, e.externalActive && e.op == nil
}

func (e *threadExecution) reconcileIdleResume(generation uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// The explicit idle reply covers the fence observed when this resume
	// started. It supplies no terminal proof for reserved SDK work and
	// cannot clear newer provider activity.
	if e.externalActive && e.op == nil && !e.closed.Load() && e.activeGeneration == generation {
		e.clearExternalActivityLocked()
	}
}

// A resume response can reveal activity that began before the SDK observed
// a root start. Keep independently known ownership and matching terminals.
func (e *threadExecution) reconcileExternalActivity(incoming *threadExecution) {
	if incoming == nil || !incoming.externalActive {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	active := incoming.activeID()
	if active != "" {
		if _, seen := e.fencedTerminals[active]; seen && e.externalActive && e.activeID() == "" {
			e.clearExternalActivityLocked()
			return
		}
		if active == e.lastTerminalID {
			return
		}
	}
	if current := e.activeID(); current != "" {
		if active == "" || current == active {
			return
		}
		// Conflicting reports cannot identify an interrupt target.
		active = ""
	}
	e.fenceExternalActivityLocked(active)
}

func (e *threadExecution) fenceExternalActivityLocked(turnID string) {
	e.externalActive = true
	e.activeGeneration++
	e.activeTurnID.Store(turnID)
	e.fencedTerminals = nil
	e.private = false
	e.invalidateUsageLocked()
	e.signalLocked()
}

func (e *threadExecution) clearExternalActivityLocked() {
	e.externalActive = false
	e.fencedTerminals = nil
	e.activeTurnID.Store("")
	e.signalLocked()
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

// observe returns true when only the lossy stream queue drops an event.
func (e *threadExecution) observe(ev types.ThreadEvent) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.Load() {
		return false
	}
	op := e.op
	switch event := ev.(type) {
	case *types.TurnStarted:
		e.observeStartLocked(op, event)
	case *types.ThreadStatusChanged:
		e.observeStatusLocked(op, event.Status)
	case *types.TurnCompleted:
		e.observeTerminalLocked(op, event.TurnID, event)
		return false
	case *types.TurnFailed:
		e.observeTerminalLocked(op, event.TurnID, event)
		return false
	case *types.TokenUsageUpdated:
		e.observeUsageLocked(op, event)
	case *types.ItemCompleted:
		if _, ok := event.Item.(*types.ContextCompaction); ok {
			e.recordCompactionLocked(op, event.TurnID)
		}
	case *types.ContextCompacted:
		e.observeLegacyCompactLocked(op, event)
	}
	return e.queueEventLocked(op, ev)
}

func (e *threadExecution) observeStatusLocked(op *threadOperation, raw json.RawMessage) {
	if op != nil && op.dispatched && !op.complete {
		return
	}
	var status providerThreadStatus
	if json.Unmarshal(raw, &status) != nil {
		return
	}
	switch status.Type {
	case "active":
		if e.activeID() == "" {
			e.fenceExternalActivityLocked("")
		} else {
			e.activeGeneration++
		}
	case "idle":
		if e.externalActive {
			e.clearExternalActivityLocked()
		}
	}
}

func (e *threadExecution) observeStartLocked(op *threadOperation, event *types.TurnStarted) {
	id := event.TurnID
	if !isRootTurn(id, event.RootTurnID) || id == e.lastTerminalID {
		return
	}
	e.activeTurnID.Store(id)
	if op == nil || !op.dispatched || op.complete || (op.rootStartID != "" && op.rootStartID != id) {
		e.fenceExternalActivityLocked(id)
	} else {
		op.rootStartID = id
		if op.kind == executeCompact {
			e.compactTurnID = id
		}
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
	active := e.activeID()
	if active == id {
		e.clearExternalActivityLocked()
	} else if active == "" && e.externalActive && len(e.fencedTerminals) < ThreadInboxBuffer {
		if e.fencedTerminals == nil {
			e.fencedTerminals = make(map[string]struct{})
		}
		e.fencedTerminals[id] = struct{}{}
	}
	if op == nil || !op.dispatched || op.complete {
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
	return ""
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

func (e *threadExecution) recordCompactionLocked(op *threadOperation, turnID string) {
	e.invalidateUsageLocked()
	if op != nil && !op.complete && op.kind == executeCompact && turnID != "" && turnID == op.rootStartID {
		op.compactID = turnID
		e.finishLocked(op)
		e.signalLocked()
	}
}

func (e *threadExecution) observeLegacyCompactLocked(op *threadOperation, event *types.ContextCompacted) {
	e.recordCompactionLocked(op, event.TurnID)
	if event.TurnID == "" || event.TurnID != e.compactTurnID {
		return
	}
	if op != nil && op.kind == executeCompact && op.dispatched && op.rootStartID == event.TurnID {
		e.observeTerminalLocked(op, event.TurnID, event)
	}
	if sub := e.compactSub.Load(); sub != nil {
		snapshot := *event
		select {
		case *sub <- &snapshot:
		default:
		}
	}
}

func (e *threadExecution) queueEventLocked(op *threadOperation, ev types.ThreadEvent) bool {
	if op == nil || op.complete || !op.consuming {
		return false
	}
	select {
	case op.events <- ev:
		return false
	default:
		// Terminal state is retained separately even when this queue is full.
		return true
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
	if !e.hasMatchingTerminal(op, expected) {
		return
	}
	op.complete = true
	op.earlyTerminals = nil
	e.lastTerminalID = expected
	op.finalUsage, op.usageAvailable = op.usage.final()
	if terminal, ok := op.terminal.(*types.TurnCompleted); ok && terminal.UsagePresent {
		op.finalUsage, op.usageAvailable = terminal.Usage, true
	}
	e.baseline = op.usage.nextBaseline(op.usageAvailable && op.finalUsage == (types.TokenUsage{}))
	if op.events != nil {
		close(op.events)
	}
	e.releaseLocked(op)
}

func (e *threadExecution) exclusiveTerminalEvidence(op *threadOperation) bool {
	return e.private && op.uncertain && op.rootStartID != ""
}

func (e *threadExecution) hasMatchingTerminal(op *threadOperation, expected string) bool {
	if expected == "" || op.terminalID != expected {
		return false
	}
	// A compact reply has no ID. Require explicit compaction evidence
	// as well when this is not an exclusive thread.
	return op.kind != executeCompact || e.private || op.compactID == expected
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
	pending, err := t.client.demux.PostGuarded(ctx, method, params, func(writeCtx context.Context) error {
		return t.markDispatch(writeCtx, op)
	})
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

func (t *Thread) markDispatch(ctx context.Context, op *threadOperation) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.closed.Load() {
		return types.ErrThreadClosed
	}
	if t.op != op || t.externalActive || t.resuming != 0 || t.activeID() != "" {
		return &ThreadExecutionUnconfirmedError{ThreadID: t.id}
	}
	op.dispatched = true
	return nil
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
			if !op.complete {
				t.client.logger.Warn("provider execution remains unconfirmed after cancellation")
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
