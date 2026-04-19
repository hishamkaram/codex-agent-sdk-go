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
		{"FeatureNotEnabled+IsFeatureNotEnabled", NewFeatureNotEnabledError("experimentalApi", "thread/backgroundTerminals/clean", "requires experimentalApi capability"), IsFeatureNotEnabledError, true},
		{"MCPOAuth+IsMCPOAuth", NewMCPServerOAuthRequiredError("notion", "complete OAuth"), IsMCPServerOAuthRequiredError, true},
		{"AGENTSMD+IsAGENTSMD", NewAGENTSMDExistsError("/repo/AGENTS.md"), IsAGENTSMDExistsError, true},
		{"FeatureNotEnabled+IsAGENTSMD", NewFeatureNotEnabledError("a", "b", "c"), IsAGENTSMDExistsError, false},
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

func TestV040Errors_Messages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"FeatureNotEnabled with method",
			NewFeatureNotEnabledError("experimentalApi", "thread/backgroundTerminals/clean", "requires experimentalApi capability"),
			"thread/backgroundTerminals/clean requires experimentalApi: requires experimentalApi capability",
		},
		{
			"FeatureNotEnabled without method",
			NewFeatureNotEnabledError("foo", "", "bar"),
			"feature not enabled (foo): bar",
		},
		{
			"MCPOAuth with message",
			NewMCPServerOAuthRequiredError("notion", "user must complete OAuth"),
			`MCP server "notion" requires OAuth: user must complete OAuth`,
		},
		{
			"MCPOAuth without message",
			NewMCPServerOAuthRequiredError("github", ""),
			`MCP server "github" requires OAuth`,
		},
		{
			"AGENTSMDExists",
			NewAGENTSMDExistsError("/repo/AGENTS.md"),
			"AGENTS.md already exists at /repo/AGENTS.md (pass Overwrite: true to replace)",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestV040Errors_NilSafe(t *testing.T) {
	t.Parallel()
	var (
		e1 *FeatureNotEnabledError
		e2 *MCPServerOAuthRequiredError
		e3 *AGENTSMDExistsError
	)
	if got := e1.Error(); got != "<nil FeatureNotEnabledError>" {
		t.Errorf("nil FeatureNotEnabledError.Error() = %q", got)
	}
	if got := e2.Error(); got != "<nil MCPServerOAuthRequiredError>" {
		t.Errorf("nil MCPServerOAuthRequiredError.Error() = %q", got)
	}
	if got := e3.Error(); got != "<nil AGENTSMDExistsError>" {
		t.Errorf("nil AGENTSMDExistsError.Error() = %q", got)
	}
}
