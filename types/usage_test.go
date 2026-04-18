package types

import "testing"

func TestTokenUsage_Add(t *testing.T) {
	t.Parallel()
	a := TokenUsage{InputTokens: 10, CachedInputTokens: 5, OutputTokens: 20, ReasoningTokens: 3}
	b := TokenUsage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 100}
	got := a.Add(b)
	want := TokenUsage{InputTokens: 11, CachedInputTokens: 5, OutputTokens: 22, ReasoningTokens: 3, CacheCreationTokens: 100}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTokenUsage_AddZero(t *testing.T) {
	t.Parallel()
	a := TokenUsage{InputTokens: 10, OutputTokens: 20}
	if a.Add(TokenUsage{}) != a {
		t.Fatal("adding zero usage should be identity")
	}
}
