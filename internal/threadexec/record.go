package threadexec

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const InboxBuffer = 256

const executionReconciliationLimit = 5 * time.Second

type Kind uint8

const (
	Turn Kind = iota
	Review
	Compact
	historyAccess
)

// Record is the Client's authoritative state for one provider thread
// ID. The ordered dispatcher records evidence before publishing any event.
// The other writers are the reserved operation's request and cleanup owners.
type Record struct {
	mu           sync.Mutex
	changed      chan struct{}
	op           *Operation
	approvedPlan approvedPlanState // guarded by mu
	resuming     int               // guarded by mu; new work waits for the active-status reply

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

type Operation struct {
	wireID    string
	kind      Kind
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

func NewRecord(fresh bool) *Record {
	e := &Record{changed: make(chan struct{}), done: make(chan struct{}), private: fresh, baseline: usageBaseline{known: fresh}}
	e.activeTurnID.Store("")
	return e
}

// Close ends local consumption and releases pending compact waiters.
func (e *Record) Close() {
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

func (e *Record) signalLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
}

func (e *Record) invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.private = false
	// A gap could hide new activity, so an in-flight idle reply cannot
	// reconcile an older external fence across the gap.
	e.activeGeneration++
	e.invalidateUsageLocked()
	e.signalLocked()
}

func (e *Record) invalidateUsageLocked() {
	e.baseline = usageBaseline{}
	if e.op != nil && !e.op.complete {
		e.op.usage.broken = true
	}
}

func (e *Record) admit(ctx context.Context, threadID string, kind Kind, streamed bool, unconfirmed func(string) error) (*Operation, error) {
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
		if e.dispatchUnconfirmedLocked() {
			e.mu.Unlock()
			return nil, unconfirmed(threadID)
		}
		if op == nil && e.resuming == 0 {
			reserved, reserveErr := e.reserveLocked(threadID, kind, streamed)
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

// dispatchUnconfirmedLocked reports whether unresolved provider work prevents
// this execution record from accepting a new turn. The caller holds e.mu.
func (e *Record) dispatchUnconfirmedLocked() bool {
	op := e.op
	return e.approvedPlan != approvedPlanIdle || e.externalActive ||
		op != nil && !op.complete && (op.uncertain || !op.waitUntil.IsZero() && !time.Now().Before(op.waitUntil))
}

func (e *Record) reserveLocked(threadID string, kind Kind, streamed bool) (*Operation, error) {
	op := &Operation{wireID: threadID, kind: kind, consuming: streamed, usage: newTurnUsageLedger(e.baseline)}
	if streamed {
		op.events = make(chan types.ThreadEvent, InboxBuffer)
	}
	if kind == Compact {
		ch := make(chan *types.ContextCompacted, 1)
		if !e.compactSub.CompareAndSwap(nil, &ch) {
			return nil, types.ErrCompactResultPending
		}
		op.compactCh = &ch
		e.compactTurnID = ""
	}
	e.op = op
	e.signalLocked()
	return op, nil
}

func (e *Record) waitForAdmission(ctx context.Context, change <-chan struct{}, waitUntil time.Time) error {
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
func (e *Record) reject(op *Operation) {
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

func (e *Record) acknowledge(op *Operation, result json.RawMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var reply struct {
		Turn struct {
			ID         string `json:"id"`
			RootTurnID string `json:"rootTurnId"`
		} `json:"turn"`
	}
	if op.kind != Compact {
		_ = json.Unmarshal(result, &reply)
	}
	op.ackID = reply.Turn.ID
	op.acknowledged = true
	if op.ackID != "" && !IsRootTurn(op.ackID, reply.Turn.RootTurnID) {
		e.private = false
		op.usage.broken = true
	}
	if op.kind == Compact && op.waitUntil.IsZero() {
		op.waitUntil = time.Now().Add(executionReconciliationLimit)
	}
	if op.ackID != "" && op.rootStartID != "" && op.ackID != op.rootStartID {
		e.externalActive = e.ActiveID() == op.rootStartID
		e.private = false
		op.usage.broken = true
	}
	if op.ackID != "" && op.rootStartID == "" && op.terminalID != op.ackID && op.earlyTerminals[op.ackID] == nil && e.ActiveID() == "" {
		e.activeTurnID.Store(op.ackID)
	}
	e.finishLocked(op)
	e.signalLocked()
}

type providerThreadStatus struct {
	Type string `json:"type"`
}

// SeedExternalActivity is used before publishing a resumed or forked
// handle. An active status without a unique ID never permits terminal
// notifications for arbitrary turns to release admission.
func (e *Record) SeedExternalActivity(result json.RawMessage) (status, statusThreadID string) {
	var reply struct {
		Thread struct {
			ID     string               `json:"id"`
			Status providerThreadStatus `json:"status"`
			Turns  []struct {
				ID         string `json:"id"`
				RootTurnID string `json:"rootTurnId"`
				Status     string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &reply) != nil {
		return "", ""
	}
	status, statusThreadID = reply.Thread.Status.Type, reply.Thread.ID
	if status != "active" {
		return status, statusThreadID
	}
	e.externalActive = true
	active := ""
	for _, turn := range reply.Thread.Turns {
		if turn.Status == "inProgress" && IsRootTurn(turn.ID, turn.RootTurnID) {
			if active != "" {
				return status, statusThreadID
			}
			active = turn.ID
		}
	}
	e.activeTurnID.Store(active)
	return status, statusThreadID
}

// ResumeIdleCandidate captures the generation before an ordered resume reply.
func (e *Record) ResumeIdleCandidate() (generation uint64, eligible bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeGeneration, e.externalActive && e.op == nil
}

// ReconcileIdleResume accepts evidence only for the captured generation.
func (e *Record) ReconcileIdleResume(generation uint64) {
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
func (e *Record) reconcileExternalActivity(incoming *Record) {
	if incoming == nil || !incoming.externalActive {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	active := incoming.ActiveID()
	if active != "" {
		if _, seen := e.fencedTerminals[active]; seen && e.externalActive && e.ActiveID() == "" {
			e.clearExternalActivityLocked()
			return
		}
		if active == e.lastTerminalID {
			return
		}
	}
	if current := e.ActiveID(); current != "" {
		if active == "" || current == active {
			return
		}
		// Conflicting reports cannot identify an interrupt target.
		active = ""
	}
	e.fenceExternalActivityLocked(active)
}

func (e *Record) fenceExternalActivityLocked(turnID string) {
	e.externalActive = true
	e.activeGeneration++
	e.activeTurnID.Store(turnID)
	e.fencedTerminals = nil
	e.private = false
	e.invalidateUsageLocked()
	e.signalLocked()
}

func (e *Record) clearExternalActivityLocked() {
	e.externalActive = false
	e.fencedTerminals = nil
	e.activeTurnID.Store("")
	e.signalLocked()
}

func IsRootTurn(id, rootID string) bool {
	return id != "" && (rootID == "" || rootID == id)
}

func (e *Record) ActiveID() string {
	id, _ := e.activeTurnID.Load().(string)
	return id
}

func (e *Record) consumeDone(op *Operation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op.consuming = false
	e.releaseLocked(op)
	e.signalLocked()
}

func (e *Record) releaseLocked(op *Operation) {
	if e.op == op && op.complete && !op.consuming && !op.cleaning {
		e.op = nil
	}
}

func (e *Record) Closed() bool { return e.closed.Load() }

func (op *Operation) AckID() string { return op.ackID }

func (e *Record) Final(op *Operation) (types.TokenUsage, bool, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return op.finalUsage, op.usageAvailable, op.complete
}
