package types

import (
	"testing"
	"time"
)

// recordingObserver embeds NopObserver and records the events it cares about,
// proving the forward-compatible embedding pattern works.
type recordingObserver struct {
	NopObserver
	connectCalls int
}

func (r *recordingObserver) OnConnect(time.Duration, error) { r.connectCalls++ }

func TestCodexOptions_ObserverOrNop(t *testing.T) {
	t.Parallel()

	t.Run("nil receiver returns NopObserver", func(t *testing.T) {
		t.Parallel()
		var o *CodexOptions
		got := o.ObserverOrNop()
		if _, ok := got.(NopObserver); !ok {
			t.Fatalf("nil receiver ObserverOrNop = %T, want NopObserver", got)
		}
	})

	t.Run("unset Observer returns NopObserver", func(t *testing.T) {
		t.Parallel()
		o := NewCodexOptions()
		got := o.ObserverOrNop()
		if _, ok := got.(NopObserver); !ok {
			t.Fatalf("unset ObserverOrNop = %T, want NopObserver", got)
		}
	})

	t.Run("set Observer is returned as-is", func(t *testing.T) {
		t.Parallel()
		rec := &recordingObserver{}
		o := NewCodexOptions().WithObserver(rec)
		got := o.ObserverOrNop()
		if got != rec {
			t.Fatalf("ObserverOrNop returned a different Observer than was set")
		}
		// And the embedding works: calling an overridden method records.
		got.OnConnect(time.Millisecond, nil)
		// And a non-overridden method is a safe no-op via the embedded NopObserver.
		got.OnBackpressure()
		if rec.connectCalls != 1 {
			t.Fatalf("connectCalls = %d, want 1", rec.connectCalls)
		}
	})

	t.Run("WithObserver(nil) restores NopObserver semantics", func(t *testing.T) {
		t.Parallel()
		o := NewCodexOptions().WithObserver(&recordingObserver{}).WithObserver(nil)
		got := o.ObserverOrNop()
		if _, ok := got.(NopObserver); !ok {
			t.Fatalf("after WithObserver(nil), ObserverOrNop = %T, want NopObserver", got)
		}
	})
}

func TestCodexOptions_WithMaxConsecutiveParseErrors(t *testing.T) {
	t.Parallel()

	o := NewCodexOptions()
	if o.MaxConsecutiveParseErrors != nil {
		t.Fatalf("default MaxConsecutiveParseErrors = %v, want nil", o.MaxConsecutiveParseErrors)
	}

	o.WithMaxConsecutiveParseErrors(5)
	if o.MaxConsecutiveParseErrors == nil {
		t.Fatal("MaxConsecutiveParseErrors still nil after WithMaxConsecutiveParseErrors(5)")
	}
	if *o.MaxConsecutiveParseErrors != 5 {
		t.Fatalf("MaxConsecutiveParseErrors = %d, want 5", *o.MaxConsecutiveParseErrors)
	}

	// Chaining returns the receiver.
	if o.WithMaxConsecutiveParseErrors(7) != o {
		t.Fatal("WithMaxConsecutiveParseErrors did not return the receiver")
	}
	if *o.MaxConsecutiveParseErrors != 7 {
		t.Fatalf("MaxConsecutiveParseErrors = %d, want 7", *o.MaxConsecutiveParseErrors)
	}
}
