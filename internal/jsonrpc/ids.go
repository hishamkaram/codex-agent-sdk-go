// Package jsonrpc provides minimal JSON-RPC 2.0 primitives for the Codex
// app-server transport: monotonic ID allocation, envelope types, a
// line-framed writer, and a multiplexing reader that classifies inbound
// frames into responses, notifications, and server-initiated requests.
//
// Wire quirks (verified against real codex app-server transcripts):
//   - Line-delimited JSON, LF terminator.
//   - The "jsonrpc":"2.0" field is OMITTED on the wire — the server tolerates
//     absence. Saves bytes and matches the upstream Python SDK.
//   - Read buffer minimum 2 MiB: user input cap is 1 MiB but envelope
//     overhead pushes some notifications past that.
package jsonrpc

import "sync/atomic"

// IDAllocator produces monotonically-increasing request IDs starting at 1.
// Safe for concurrent use.
type IDAllocator struct {
	next atomic.Uint64
}

// Next returns the next ID. IDs start at 1 (never 0 — 0 is reserved as "no id").
func (a *IDAllocator) Next() uint64 {
	return a.next.Add(1)
}
