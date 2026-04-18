package types

// TokenUsage accounts for the tokens consumed during a turn.
//
// Cached vs non-cached tokens: when the OpenAI backend reuses a previous
// prompt cache, the billed input tokens are counted in InputTokens and
// the cached portion is ALSO reported in CachedInputTokens (same tokens,
// different accounting) — follow the server's semantics.
type TokenUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	CachedInputTokens   int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

// Add returns the sum of two TokenUsage values. Useful for running totals.
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	return TokenUsage{
		InputTokens:         u.InputTokens + other.InputTokens,
		CachedInputTokens:   u.CachedInputTokens + other.CachedInputTokens,
		OutputTokens:        u.OutputTokens + other.OutputTokens,
		ReasoningTokens:     u.ReasoningTokens + other.ReasoningTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
	}
}
