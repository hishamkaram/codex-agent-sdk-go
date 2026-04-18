package types

import (
	"context"
	"testing"
)

func TestHookDecisionMarkers(t *testing.T) {
	t.Parallel()
	// Compile-time: all three decisions satisfy HookDecision.
	var _ HookDecision = HookAllow{}
	var _ HookDecision = HookDeny{Reason: "x"}
	var _ HookDecision = HookAsk{}
}

func TestDefaultAllowHookHandler(t *testing.T) {
	t.Parallel()
	got := DefaultAllowHookHandler(context.Background(), HookInput{HookEventName: HookPreToolUse})
	if _, ok := got.(HookAllow); !ok {
		t.Fatalf("got %T, want HookAllow", got)
	}
}

func TestHookEventMethods(t *testing.T) {
	t.Parallel()
	if (&HookStarted{}).EventMethod() != "hook/started" {
		t.Fatal("HookStarted.EventMethod")
	}
	if (&HookCompleted{}).EventMethod() != "hook/completed" {
		t.Fatal("HookCompleted.EventMethod")
	}
}
