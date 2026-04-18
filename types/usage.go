package types

// TokenUsage accounts for tokens consumed during a turn. Field names and
// JSON tags match the codex server's camelCase wire format (verified
// against captured transcripts from CLI 0.121.0).
//
// Two accounting scopes exist on the wire:
//   - "last"  — tokens for the most recent turn alone
//   - "total" — cumulative across the thread's lifetime
//
// The SDK surfaces "last" on TurnCompleted.Usage and "total" on
// TokenUsageUpdated.Usage — both share this struct shape.
type TokenUsage struct {
	TotalTokens           int64 `json:"totalTokens,omitempty"`
	InputTokens           int64 `json:"inputTokens,omitempty"`
	CachedInputTokens     int64 `json:"cachedInputTokens,omitempty"`
	OutputTokens          int64 `json:"outputTokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens,omitempty"`
}

// Add returns the sum of two TokenUsage values. Useful for running totals.
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	return TokenUsage{
		TotalTokens:           u.TotalTokens + other.TotalTokens,
		InputTokens:           u.InputTokens + other.InputTokens,
		CachedInputTokens:     u.CachedInputTokens + other.CachedInputTokens,
		OutputTokens:          u.OutputTokens + other.OutputTokens,
		ReasoningOutputTokens: u.ReasoningOutputTokens + other.ReasoningOutputTokens,
	}
}
