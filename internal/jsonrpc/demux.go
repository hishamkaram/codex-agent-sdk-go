package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"go.uber.org/zap"
)

// ErrClosed is returned by Send when the demux has been closed.
var ErrClosed = errors.New("jsonrpc: demux closed")

// Demux reads JSON-RPC frames from a LineReader and classifies each one:
//   - {id, result|error}      → response to a client-initiated request →
//     delivered to the pending[id] channel created by Send.
//   - {id, method}            → server-initiated request → pushed to the
//     ServerRequests channel for the caller to handle.
//   - {method} (no id)        → notification → pushed to the Notifications
//     channel.
//
// The demux spawns exactly one goroutine (the read loop) and owns the
// channels it returns. Close stops the read loop and closes all channels.
type Demux struct {
	reader *LineReader
	writer *LineWriter
	logger *sdklog.Logger
	ids    IDAllocator

	mu      sync.Mutex
	pending map[uint64]chan Response
	closed  bool

	notifications  chan Notification
	serverRequests chan ServerRequest
	loopErr        chan error

	stopOnce sync.Once
	stopped  chan struct{}
}

// NewDemux constructs a Demux. The caller retains ownership of r and w; the
// demux reads r in a goroutine (started by Run) but never closes it — that
// is the transport's responsibility.
func NewDemux(r *LineReader, w *LineWriter, logger *sdklog.Logger) *Demux {
	if logger == nil {
		logger = sdklog.NewLoggerFromZap(nil)
	}
	return &Demux{
		reader:         r,
		writer:         w,
		logger:         logger,
		pending:        make(map[uint64]chan Response),
		notifications:  make(chan Notification, 64),
		serverRequests: make(chan ServerRequest, 16),
		loopErr:        make(chan error, 1),
		stopped:        make(chan struct{}),
	}
}

// Notifications returns the channel of server-sent notifications.
// Closed by Close.
func (d *Demux) Notifications() <-chan Notification { return d.notifications }

// ServerRequests returns the channel of server-initiated requests
// (approvals, elicitations). Closed by Close. The caller MUST respond to
// every request via RespondServerRequest before close.
func (d *Demux) ServerRequests() <-chan ServerRequest { return d.serverRequests }

// LoopError returns a channel that receives exactly one error value when
// the read loop exits (nil on clean EOF). Buffered size 1.
func (d *Demux) LoopError() <-chan error { return d.loopErr }

// Run starts the read loop in a goroutine. Safe to call exactly once.
// The ctx is used only for logging/cancellation visibility — the loop exits
// on io.EOF from the reader (triggered by Close).
func (d *Demux) Run(ctx context.Context) {
	go d.readLoop(ctx)
}

// Send sends a client-initiated Request and blocks until the matching
// Response arrives, ctx is canceled, or the demux closes. On success the
// Response is returned as-is (including any server-side error in
// Response.Error).
func (d *Demux) Send(ctx context.Context, method string, params any) (Response, error) {
	id := d.ids.Next()

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return Response{}, fmt.Errorf("jsonrpc.Demux.Send: marshal params: %w", err)
		}
		paramsRaw = b
	}

	ch := make(chan Response, 1)

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return Response{}, ErrClosed
	}
	d.pending[id] = ch
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
	}()

	req := Request{ID: id, Method: method, Params: paramsRaw}
	if err := d.writer.WriteFrame(req); err != nil {
		return Response{}, fmt.Errorf("jsonrpc.Demux.Send: write: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return Response{}, fmt.Errorf("jsonrpc.Demux.Send: %w", ctx.Err())
	case <-d.stopped:
		return Response{}, ErrClosed
	}
}

// Notify sends a client-to-server notification (no response expected).
func (d *Demux) Notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("jsonrpc.Demux.Notify: marshal params: %w", err)
		}
		paramsRaw = b
	}
	n := Notification{Method: method, Params: paramsRaw}
	if err := d.writer.WriteFrame(n); err != nil {
		return fmt.Errorf("jsonrpc.Demux.Notify: write: %w", err)
	}
	return nil
}

// RespondServerRequest sends a response to a server-initiated request.
// result may be nil for responses that carry no payload. If rpcErr is
// non-nil, it takes precedence over result.
func (d *Demux) RespondServerRequest(id uint64, result any, rpcErr *RPCError) error {
	var resultRaw json.RawMessage
	if result != nil && rpcErr == nil {
		b, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("jsonrpc.Demux.RespondServerRequest: marshal result: %w", err)
		}
		resultRaw = b
	}
	resp := Response{ID: id, Result: resultRaw, Error: rpcErr}
	if err := d.writer.WriteFrame(resp); err != nil {
		return fmt.Errorf("jsonrpc.Demux.RespondServerRequest: write: %w", err)
	}
	return nil
}

// Close stops the read loop and unblocks any in-flight Send calls.
// Idempotent.
func (d *Demux) Close() error {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		// Unblock every pending Send by closing its channel without delivering.
		for id, ch := range d.pending {
			close(ch)
			delete(d.pending, id)
		}
		d.mu.Unlock()
		close(d.stopped)
	})
	return nil
}

// readLoop runs on a dedicated goroutine. Exits on io.EOF or unrecoverable
// read error, delivering the terminal error to LoopError and closing all
// outbound channels.
func (d *Demux) readLoop(ctx context.Context) {
	var exitErr error
	defer func() {
		d.loopErr <- exitErr
		close(d.notifications)
		close(d.serverRequests)
	}()

	for {
		line, err := d.reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			exitErr = err
			return
		}
		if len(line) == 0 {
			continue
		}

		var frame rawFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			d.logger.Warn("jsonrpc.Demux: malformed inbound frame",
				zap.Error(err),
				zap.ByteString("line", truncate(line, 512)))
			continue
		}

		switch {
		case frame.Method != nil && frame.ID != nil:
			// Server-initiated request.
			req := ServerRequest{ID: *frame.ID, Method: *frame.Method, Params: frame.Params}
			select {
			case d.serverRequests <- req:
			case <-d.stopped:
				return
			case <-ctx.Done():
				return
			}

		case frame.Method != nil:
			// Notification.
			note := Notification{Method: *frame.Method, Params: frame.Params}
			select {
			case d.notifications <- note:
			case <-d.stopped:
				return
			case <-ctx.Done():
				return
			}

		case frame.ID != nil:
			// Response to a client-initiated request.
			resp := Response{ID: *frame.ID, Result: frame.Result, Error: frame.Error}
			d.mu.Lock()
			ch, ok := d.pending[*frame.ID]
			d.mu.Unlock()
			if !ok {
				d.logger.Warn("jsonrpc.Demux: unsolicited response",
					zap.Uint64("id", *frame.ID))
				continue
			}
			// ch is buffered size 1; this never blocks.
			ch <- resp

		default:
			d.logger.Warn("jsonrpc.Demux: unclassifiable frame",
				zap.ByteString("line", truncate(line, 512)))
		}
	}
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
