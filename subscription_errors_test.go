package codex

import (
	"errors"
	"testing"
)

func TestThreadEventGapErrorDiagnostics(t *testing.T) {
	t.Parallel()

	cause := errors.New("fixture cause")
	for _, test := range []struct {
		name string
		gap  error
		want string
		wrap error
	}{
		{"overflow", &ThreadEventStreamGapError{ThreadID: "child", Buffer: 3}, `codex thread event stream gap: buffer 3 exhausted while delivering thread "child"`, nil},
		{"parse", &ThreadEventParseGapError{ThreadID: "child", Method: "item/started", Cause: cause}, `codex thread event stream gap: parse "item/started" for thread "child": fixture cause`, cause},
		{"frame", &ThreadEventFrameGapError{Cause: cause}, "codex thread event stream gap: decode inbound app-server frame: fixture cause", cause},
		{"frame without cause", &ThreadEventFrameGapError{}, "codex thread event stream gap: decode inbound app-server frame", nil},
		{"source", &ThreadEventSourceGapError{Cause: cause}, "codex thread event stream gap: app-server event source closed: fixture cause", cause},
		{"source EOF", &ThreadEventSourceGapError{}, "codex thread event stream gap: app-server event source closed", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.gap.Error(); got != test.want {
				t.Errorf("Error() = %q, want %q", got, test.want)
			}
			if !errors.Is(test.gap, ErrThreadEventStreamGap) || errors.Is(test.gap, cause) != (test.wrap != nil) {
				t.Errorf("gap did not preserve the expected error chain: %v", test.gap)
			}
		})
	}
}

func TestThreadEventParseGapWithoutCauseOmitsNilUnwrapEntry(t *testing.T) {
	t.Parallel()
	gap := &ThreadEventParseGapError{Method: "item/started"}
	causes := gap.Unwrap()
	if len(causes) != 1 || !errors.Is(causes[0], ErrThreadEventStreamGap) {
		t.Fatalf("Unwrap() = %#v, want only ErrThreadEventStreamGap", causes)
	}
}

func TestNilThreadEventGapErrorDoesNotPanicDuringTraversal(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		gap     error
		want    string
		matches bool
	}{
		{"stream", (*ThreadEventStreamGapError)(nil), "<nil ThreadEventStreamGapError>", true},
		{"parse", (*ThreadEventParseGapError)(nil), "<nil ThreadEventParseGapError>", false},
		{"frame", (*ThreadEventFrameGapError)(nil), "<nil ThreadEventFrameGapError>", false},
		{"source", (*ThreadEventSourceGapError)(nil), "<nil ThreadEventSourceGapError>", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := errors.Is(test.gap, ErrThreadEventStreamGap); got != test.matches {
				t.Errorf("errors.Is(gap, ErrThreadEventStreamGap) = %t, want %t", got, test.matches)
			}
			if got := test.gap.Error(); got != test.want {
				t.Errorf("Error() = %q, want %q", got, test.want)
			}
		})
	}
}
