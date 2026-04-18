package types

import (
	"errors"
	"fmt"
	"testing"
)

func TestTypedErrors_IsHelpers(t *testing.T) {
	t.Parallel()
	cli := NewCLINotFoundError("not found")
	conn := NewCLIConnectionError("stdin pipe", errors.New("broken"))
	proc := NewProcessError("exited", 1, "stderr tail")
	dec := NewJSONDecodeError("garbage", errors.New("bad json"))
	parse := NewMessageParseError("missing type", "{}")
	rpc := NewRPCError(-32000, "server busy", nil)
	appr := NewApprovalDeniedError("command/requestApproval", "blocked by policy")

	tests := []struct {
		name    string
		err     error
		checker func(error) bool
		want    bool
	}{
		{"CLINotFound+IsCLINotFound", cli, IsCLINotFoundError, true},
		{"CLIConn+IsCLIConn", conn, IsCLIConnectionError, true},
		{"Process+IsProcess", proc, IsProcessError, true},
		{"JSONDecode+IsJSONDecode", dec, IsJSONDecodeError, true},
		{"MsgParse+IsMsgParse", parse, IsMessageParseError, true},
		{"RPC+IsRPC", rpc, IsRPCError, true},
		{"Approval+IsApproval", appr, IsApprovalDeniedError, true},
		{"CLINotFound+IsProcess", cli, IsProcessError, false},
		{"nil+IsCLINotFound", nil, IsCLINotFoundError, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.checker(tt.err); got != tt.want {
				t.Fatalf("got %v, want %v (err=%v)", got, tt.want, tt.err)
			}
		})
	}
}

func TestTypedErrors_Wrapping(t *testing.T) {
	t.Parallel()
	inner := NewCLIConnectionError("inner", errors.New("root cause"))
	wrapped := fmt.Errorf("outer: %w", inner)
	if !IsCLIConnectionError(wrapped) {
		t.Fatal("IsCLIConnectionError must see through fmt.Errorf wrapping")
	}
	var e *CLIConnectionError
	if !errors.As(wrapped, &e) {
		t.Fatal("errors.As must extract *CLIConnectionError")
	}
	if e.Message != "inner" {
		t.Fatalf("unexpected Message: %q", e.Message)
	}
	// Unwrap chain reaches the root cause.
	if !errors.Is(wrapped, errors.Unwrap(inner)) {
		t.Fatal("errors.Is must see the root cause through nested wrapping")
	}
}

func TestCLINotFound_NilSafe(t *testing.T) {
	t.Parallel()
	var e *CLINotFoundError
	if got := e.Error(); got != "<nil CLINotFoundError>" {
		t.Fatalf("nil.Error() = %q", got)
	}
}

func TestJSONDecodeError_TruncatesRaw(t *testing.T) {
	t.Parallel()
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'A'
	}
	e := NewJSONDecodeError(string(long), errors.New("bad"))
	s := e.Error()
	if len(s) > 400 {
		t.Fatalf("Error() output too long: %d bytes", len(s))
	}
}
