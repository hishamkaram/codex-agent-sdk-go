package jsonrpc

import (
	"encoding/json"
	"errors"
	"strconv"
)

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
	// DecodeError marks a synthetic, stream-ordered gap emitted when an
	// inbound frame cannot be decoded or classified. It is never serialized.
	DecodeError error `json:"-"`
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
// Its ID is a validated JSON string or signed 64-bit integer token, kept
// separately from the SDK's uint64 client-request IDs.
type ServerRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type serverResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
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
// line. Decode the ID only after distinguishing a request from a reply.
type rawFrame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method *string         `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`

	responseID uint64
}

func decodeFrame(line []byte) (rawFrame, error) {
	var frame rawFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		return rawFrame{}, err
	}
	if string(frame.ID) == "null" {
		return rawFrame{}, errors.New("JSON-RPC ID cannot be null")
	}
	if frame.ID == nil {
		return frame, nil
	}
	if frame.Method != nil {
		if frame.ID[0] != '"' {
			if _, err := strconv.ParseInt(string(frame.ID), 10, 64); err != nil {
				return rawFrame{}, errors.New("server request ID must be a JSON string or int64")
			}
		}
		return frame, nil
	}
	if err := json.Unmarshal(frame.ID, &frame.responseID); err != nil {
		return rawFrame{}, errors.New("client response ID must be a uint64")
	}
	return frame, nil
}
