// Package transport handles the codex CLI subprocess: discovery, version
// probing, spawn/shutdown lifecycle, and wiring of stdin/stdout pipes into
// the JSON-RPC demux.
//
// The Transport interface is intentionally narrow — it owns the subprocess
// and exposes channels/send methods from the underlying Demux. Higher-level
// semantics (handshake, turns, approvals) live in the root package and
// internal/query.
package transport

import (
	"context"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
)

// Transport manages the codex app-server subprocess and exposes the
// underlying JSON-RPC demultiplexer.
type Transport interface {
	// Connect spawns the subprocess, wires pipes, and starts the demux read
	// loop. Does NOT perform the `initialize` handshake — that is the
	// caller's job via the Demux returned by Demux().
	Connect(ctx context.Context) error

	// Demux returns the active demultiplexer. Only valid after Connect and
	// before Close. May return nil otherwise.
	Demux() *jsonrpc.Demux

	// Close signals the subprocess to exit (closes stdin), waits for the
	// read loop to drain, then terminates+kills if the process does not
	// exit within the grace period. Safe to call multiple times.
	Close(ctx context.Context) error

	// Stderr returns the captured stderr tail (for diagnostics). Only
	// stable to read after Close has returned.
	Stderr() string

	// Pid returns the subprocess pid or 0 if not connected.
	Pid() int
}
