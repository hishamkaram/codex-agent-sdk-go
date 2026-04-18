package jsonrpc

import "encoding/json"

// Request is a client-initiated JSON-RPC request.
//
// Wire shape: {"id":<uint64>,"method":"...","params":...}
// The "jsonrpc":"2.0" field is intentionally omitted — the codex server
// tolerates its absence (matches the upstream Python SDK).
type Request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Notification is a client-to-server or server-to-client message that has
// no ID and expects no response.
//
// Wire shape: {"method":"...","params":...}
type Notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a reply to a client-initiated Request. Exactly one of Result
// or Error is populated.
//
// Wire shape: {"id":<uint64>,"result":...} or {"id":<uint64>,"error":{...}}
type Response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// ServerRequest is a request initiated by the server (e.g., approval prompts).
// The client MUST respond with a matching ID or the server will block.
//
// Wire shape: {"id":<uint64>,"method":"...","params":...} — same shape as
// Request but arriving from the server. Distinguished from Response by the
// presence of "method".
type ServerRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// RPCError is the "error" field of a JSON-RPC Response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	if e == nil {
		return "<nil RPCError>"
	}
	return e.Message
}

// rawFrame is the polymorphic shape used by the demux to classify an inbound
// line. Fields are pointers so presence/absence can be distinguished.
type rawFrame struct {
	ID     *uint64         `json:"id,omitempty"`
	Method *string         `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
