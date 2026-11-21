package types

// TokenUsage is a provider token-count breakdown. The containing event or
// result determines its scope. CachedInputTokens measures cache reads.
// CacheWriteInputTokens measures writes and is zero on older payloads that
// omit it. TotalTokens is the provider total and is not recomputed.
type TokenUsage struct {
	TotalTokens           int64 `json:"totalTokens,omitempty"`
	InputTokens           int64 `json:"inputTokens,omitempty"`
	CachedInputTokens     int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens,omitempty"`
	OutputTokens          int64 `json:"outputTokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens,omitempty"`
}

// Add returns the sum of two TokenUsage values. Useful for running totals.
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	return TokenUsage{
		TotalTokens:           u.TotalTokens + other.TotalTokens,
		InputTokens:           u.InputTokens + other.InputTokens,
		CachedInputTokens:     u.CachedInputTokens + other.CachedInputTokens,
		CacheWriteInputTokens: u.CacheWriteInputTokens + other.CacheWriteInputTokens,
		OutputTokens:          u.OutputTokens + other.OutputTokens,
		ReasoningOutputTokens: u.ReasoningOutputTokens + other.ReasoningOutputTokens,
	}
}
