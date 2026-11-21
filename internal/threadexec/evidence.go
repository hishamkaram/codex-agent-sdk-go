package threadexec

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Observe returns true when only the lossy stream queue drops an event.
func (e *Record) Observe(ev types.ThreadEvent) bool {
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

func (e *Record) observeStatusLocked(op *Operation, raw json.RawMessage) {
	if op != nil && op.dispatched && !op.complete {
		return
	}
	switch StatusKind(raw) {
	case "", "active":
		if e.ActiveID() == "" {
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

func StatusKind(raw json.RawMessage) string {
	var status providerThreadStatus
	if json.Unmarshal(raw, &status) == nil {
		return status.Type
	}
	// Older event payloads use a plain status string.
	var legacy string
	_ = json.Unmarshal(raw, &legacy)
	return legacy
}

func (e *Record) observeStartLocked(op *Operation, event *types.TurnStarted) {
	id := event.TurnID
	if id == "" {
		e.fenceExternalActivityLocked(e.ActiveID())
		return
	}
	if !IsRootTurn(id, event.RootTurnID) || id == e.lastTerminalID {
		return
	}
	e.activeTurnID.Store(id)
	if op == nil || !op.dispatched || op.complete || (op.rootStartID != "" && op.rootStartID != id) {
		e.fenceExternalActivityLocked(id)
	} else {
		op.rootStartID = id
		if op.kind == Compact {
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

func (e *Record) observeTerminalLocked(op *Operation, id string, ev types.ThreadEvent) {
	if id == "" || id == e.lastTerminalID {
		return
	}
	active := e.ActiveID()
	if active == id {
		e.clearExternalActivityLocked()
	} else if active == "" && e.externalActive && len(e.fencedTerminals) < InboxBuffer {
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
	} else if !op.acknowledged && len(op.earlyTerminals) < InboxBuffer {
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

func (op *Operation) expectedID() string {
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

func (op *Operation) setTerminal(id string, ev types.ThreadEvent) {
	op.terminalID = id
	op.terminal = copyTerminal(ev)
}

func (e *Record) observeUsageLocked(op *Operation, event *types.TokenUsageUpdated) {
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

func (e *Record) recordCompactionLocked(op *Operation, turnID string) {
	e.invalidateUsageLocked()
	if op != nil && !op.complete && op.kind == Compact && turnID != "" && turnID == op.rootStartID {
		op.compactID = turnID
		e.finishLocked(op)
		e.signalLocked()
	}
}

func (e *Record) observeLegacyCompactLocked(op *Operation, event *types.ContextCompacted) {
	e.recordCompactionLocked(op, event.TurnID)
	if event.TurnID == "" || event.TurnID != e.compactTurnID {
		return
	}
	if op != nil && op.kind == Compact && op.dispatched && op.rootStartID == event.TurnID {
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

func (e *Record) queueEventLocked(op *Operation, ev types.ThreadEvent) bool {
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

func (e *Record) finishLocked(op *Operation) {
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

func (e *Record) exclusiveTerminalEvidence(op *Operation) bool {
	return e.private && op.uncertain && op.rootStartID != ""
}

func (e *Record) hasMatchingTerminal(op *Operation, expected string) bool {
	if expected == "" || op.terminalID != expected {
		return false
	}
	// A compact reply has no ID. Require explicit compaction evidence
	// as well when this is not an exclusive thread.
	return op.kind != Compact || e.private || op.compactID == expected
}
