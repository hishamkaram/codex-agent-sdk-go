package codex

import "github.com/hishamkaram/codex-agent-sdk-go/types"

// usageBaseline is stored on the execution record. Its window is a same-epoch
// check, never sufficient to establish trust on its own.
type usageBaseline struct {
	total  types.TokenUsage
	window int64
	known  bool
}

// turnUsageLedger is private to one matched provider operation.
type turnUsageLedger struct {
	start   usageBaseline
	latest  usageBaseline
	seen    bool
	advance bool
	broken  bool
}

func newTurnUsageLedger(baseline usageBaseline) turnUsageLedger {
	return turnUsageLedger{start: baseline, latest: baseline}
}

func (l *turnUsageLedger) record(ev *types.TokenUsageUpdated) {
	if l.broken {
		return
	}
	// The core replaces the counters with only totalTokens when filling the
	// context window. That synthetic value cannot measure consumed tokens.
	if !ev.TotalUsagePresent || !ev.LastUsagePresent || syntheticUsageTotal(ev.TotalUsage) {
		l.broken = true
		return
	}
	now := usageBaseline{total: ev.TotalUsage, window: ev.ModelContextWindow, known: true}
	if l.latest.known {
		if l.latest.window != 0 && l.latest.window != now.window {
			l.broken = true
			return
		}
		if l.latest.total != now.total {
			increment, ok := usageDifference(now.total, l.latest.total)
			if !ok || increment != ev.LastUsage {
				l.broken = true
				return
			}
			l.advance = true
		}
	}
	l.seen = true
	l.latest = now
}

func (l turnUsageLedger) final() (types.TokenUsage, bool) {
	if l.broken || !l.start.known || !l.seen || !l.advance {
		return types.TokenUsage{}, false
	}
	return usageDifference(l.latest.total, l.start.total)
}

func (l turnUsageLedger) nextBaseline() usageBaseline {
	if l.broken {
		return usageBaseline{}
	}
	if l.advance {
		return l.latest
	}
	// Replays and silent turns do not establish a new measurement. The
	// next advance must still equal its independent LastUsage sample.
	return l.start
}

func syntheticUsageTotal(total types.TokenUsage) bool {
	// This shape also guards against truncated positive totals.
	if total.TotalTokens <= 0 {
		return false
	}
	total.TotalTokens = 0
	return total == (types.TokenUsage{})
}

func usageDifference(now, before types.TokenUsage) (types.TokenUsage, bool) {
	if before.TotalTokens < 0 || before.InputTokens < 0 || before.CachedInputTokens < 0 ||
		before.CacheWriteInputTokens < 0 || before.OutputTokens < 0 || before.ReasoningOutputTokens < 0 ||
		now.TotalTokens < before.TotalTokens || now.InputTokens < before.InputTokens ||
		now.CachedInputTokens < before.CachedInputTokens ||
		now.CacheWriteInputTokens < before.CacheWriteInputTokens ||
		now.OutputTokens < before.OutputTokens || now.ReasoningOutputTokens < before.ReasoningOutputTokens {
		return types.TokenUsage{}, false
	}
	return types.TokenUsage{
		TotalTokens:           now.TotalTokens - before.TotalTokens,
		InputTokens:           now.InputTokens - before.InputTokens,
		CachedInputTokens:     now.CachedInputTokens - before.CachedInputTokens,
		CacheWriteInputTokens: now.CacheWriteInputTokens - before.CacheWriteInputTokens,
		OutputTokens:          now.OutputTokens - before.OutputTokens,
		ReasoningOutputTokens: now.ReasoningOutputTokens - before.ReasoningOutputTokens,
	}, true
}
