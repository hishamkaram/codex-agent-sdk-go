// Package types defines the public API types for the Codex Agent SDK: events,
// items, options, approvals, and typed errors with Is*() helpers.
package types

import (
	"errors"
	"fmt"
)

// CLINotFoundError is returned when the codex CLI binary cannot be located or
// does not meet the SDK's minimum version requirement.
type CLINotFoundError struct {
	Message string
}

// Error implements the error interface.
func (e *CLINotFoundError) Error() string {
	if e == nil {
		return "<nil CLINotFoundError>"
	}
	return e.Message
}

// NewCLINotFoundError constructs a CLINotFoundError.
func NewCLINotFoundError(msg string) *CLINotFoundError {
	return &CLINotFoundError{Message: msg}
}

// IsCLINotFoundError reports whether err is a *CLINotFoundError.
func IsCLINotFoundError(err error) bool {
	var e *CLINotFoundError
	return errors.As(err, &e)
}

// CLIConnectionError is returned when the subprocess pipe setup, spawn, or
// initial JSON-RPC handshake fails.
type CLIConnectionError struct {
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *CLIConnectionError) Error() string {
	if e == nil {
		return "<nil CLIConnectionError>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// Unwrap exposes the wrapped cause for errors.Is / errors.As.
func (e *CLIConnectionError) Unwrap() error { return e.Cause }

// NewCLIConnectionError constructs a CLIConnectionError.
func NewCLIConnectionError(msg string, cause error) *CLIConnectionError {
	return &CLIConnectionError{Message: msg, Cause: cause}
}

// IsCLIConnectionError reports whether err is a *CLIConnectionError.
func IsCLIConnectionError(err error) bool {
	var e *CLIConnectionError
	return errors.As(err, &e)
}

// ProcessError is returned when the codex subprocess exits non-zero or is
// killed by a signal. The captured stderr tail is included for diagnostics.
type ProcessError struct {
	Message  string
	ExitCode int
	Stderr   string
}

// Error implements the error interface.
func (e *ProcessError) Error() string {
	if e == nil {
		return "<nil ProcessError>"
	}
	if e.Stderr != "" {
		return fmt.Sprintf("%s: exit=%d stderr=%q", e.Message, e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("%s: exit=%d", e.Message, e.ExitCode)
}

// NewProcessError constructs a ProcessError.
func NewProcessError(msg string, exitCode int, stderr string) *ProcessError {
	return &ProcessError{Message: msg, ExitCode: exitCode, Stderr: stderr}
}

// IsProcessError reports whether err is a *ProcessError.
func IsProcessError(err error) bool {
	var e *ProcessError
	return errors.As(err, &e)
}

// JSONDecodeError wraps a JSON parse failure on inbound CLI output.
type JSONDecodeError struct {
	Raw   string
	Cause error
}

// Error implements the error interface.
func (e *JSONDecodeError) Error() string {
	if e == nil {
		return "<nil JSONDecodeError>"
	}
	snippet := e.Raw
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return fmt.Sprintf("json decode: %v (raw=%q)", e.Cause, snippet)
}

// Unwrap exposes the wrapped cause.
func (e *JSONDecodeError) Unwrap() error { return e.Cause }

// NewJSONDecodeError constructs a JSONDecodeError.
func NewJSONDecodeError(raw string, cause error) *JSONDecodeError {
	return &JSONDecodeError{Raw: raw, Cause: cause}
}

// IsJSONDecodeError reports whether err is a *JSONDecodeError.
func IsJSONDecodeError(err error) bool {
	var e *JSONDecodeError
	return errors.As(err, &e)
}

// MessageParseError is returned when a message is valid JSON but does not
// match the expected SDK shape (missing discriminator, wrong type, etc.).
type MessageParseError struct {
	Message string
	Raw     string
}

// Error implements the error interface.
func (e *MessageParseError) Error() string {
	if e == nil {
		return "<nil MessageParseError>"
	}
	snippet := e.Raw
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return fmt.Sprintf("message parse: %s (raw=%q)", e.Message, snippet)
}

// NewMessageParseError constructs a MessageParseError.
func NewMessageParseError(msg, raw string) *MessageParseError {
	return &MessageParseError{Message: msg, Raw: raw}
}

// IsMessageParseError reports whether err is a *MessageParseError.
func IsMessageParseError(err error) bool {
	var e *MessageParseError
	return errors.As(err, &e)
}

// RPCError is returned when a JSON-RPC request receives a server-side error
// response ({"error":{"code":-32000,"message":"...",...}}).
type RPCError struct {
	Code    int
	Message string
	Data    []byte
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	if e == nil {
		return "<nil RPCError>"
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// NewRPCError constructs an RPCError.
func NewRPCError(code int, message string, data []byte) *RPCError {
	return &RPCError{Code: code, Message: message, Data: data}
}

// IsRPCError reports whether err is a *RPCError.
func IsRPCError(err error) bool {
	var e *RPCError
	return errors.As(err, &e)
}

// ApprovalDeniedError is returned when a caller's approval callback denied a
// server-initiated request. Useful for callers that want to distinguish
// "deny" from other turn failures.
type ApprovalDeniedError struct {
	Method string
	Reason string
}

// Error implements the error interface.
func (e *ApprovalDeniedError) Error() string {
	if e == nil {
		return "<nil ApprovalDeniedError>"
	}
	if e.Reason == "" {
		return fmt.Sprintf("approval denied: %s", e.Method)
	}
	return fmt.Sprintf("approval denied for %s: %s", e.Method, e.Reason)
}

// NewApprovalDeniedError constructs an ApprovalDeniedError.
func NewApprovalDeniedError(method, reason string) *ApprovalDeniedError {
	return &ApprovalDeniedError{Method: method, Reason: reason}
}

// IsApprovalDeniedError reports whether err is a *ApprovalDeniedError.
func IsApprovalDeniedError(err error) bool {
	var e *ApprovalDeniedError
	return errors.As(err, &e)
}
