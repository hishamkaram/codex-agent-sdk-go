package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// ErrClosed is returned by Send when the demux has been closed.
var ErrClosed = errors.New("jsonrpc: demux closed")

// DefaultMaxConsecutiveParseErrors is the demux's default give-up threshold:
// after this many consecutive inbound decode failures the read loop terminates
// the subprocess as unrecoverable rather than spinning on garbage forever.
const DefaultMaxConsecutiveParseErrors uint = 10

// ErrParseGiveUp is the terminal error surfaced via LoopError when the demux
// gives up after crossing the consecutive-parse-error threshold.
var ErrParseGiveUp = errors.New("jsonrpc: too many consecutive decode errors, giving up")

// ErrUnclassifiableFrame marks valid JSON that cannot be assigned to any
// supported JSON-RPC frame shape.
var ErrUnclassifiableFrame = errors.New("jsonrpc: unclassifiable inbound frame")

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

	// Observability + reliability. observer is never nil (defaults to
	// NopObserver). maxParseErrors is the give-up threshold. onUnrecoverable is
	// invoked once when the loop gives up, so the transport can kill the
	// subprocess; nil is a no-op.
	observer        types.Observer
	maxParseErrors  uint
	onUnrecoverable func(error)

	mu      sync.Mutex
	pending map[uint64]chan Response
	closed  bool

	notifications  chan Notification
	serverRequests chan ServerRequest
	loopErr        chan error

	stopOnce sync.Once
	stopped  chan struct{}
}

// DemuxOption configures optional Demux behavior (observability, reliability).
type DemuxOption func(*Demux)

// WithObserver injects the telemetry Observer. nil restores NopObserver
// semantics.
func WithObserver(obs types.Observer) DemuxOption {
	return func(d *Demux) {
		if obs == nil {
			obs = types.NopObserver{}
		}
		d.observer = obs
	}
}

// WithMaxParseErrors sets the consecutive-decode-error give-up threshold. A
// value of 0 keeps the default (DefaultMaxConsecutiveParseErrors).
func WithMaxParseErrors(n uint) DemuxOption {
	return func(d *Demux) {
		if n > 0 {
			d.maxParseErrors = n
		}
	}
}

// WithUnrecoverableHandler registers a callback invoked exactly once when the
// read loop gives up on sustained decode failures. The transport wires this to
// kill the subprocess so a parse give-up does not leave a zombie. nil is a
// no-op.
func WithUnrecoverableHandler(fn func(error)) DemuxOption {
	return func(d *Demux) { d.onUnrecoverable = fn }
}

// NewDemux constructs a Demux. The caller retains ownership of r and w; the
// demux reads r in a goroutine (started by Run) but never closes it — that
// is the transport's responsibility.
func NewDemux(r *LineReader, w *LineWriter, logger *sdklog.Logger, opts ...DemuxOption) *Demux {
	if logger == nil {
		logger = sdklog.NewLoggerFromZap(nil)
	}
	d := &Demux{
		reader:         r,
		writer:         w,
		logger:         logger,
		observer:       types.NopObserver{},
		maxParseErrors: DefaultMaxConsecutiveParseErrors,
		pending:        make(map[uint64]chan Response),
		notifications:  make(chan Notification, 64),
		serverRequests: make(chan ServerRequest, 16),
		loopErr:        make(chan error, 1),
		stopped:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
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
		// Drop every pending entry. Closing d.stopped (below) is what actually
		// unblocks the waiters — each Send selects on <-d.stopped and returns
		// ErrClosed. We must NOT close(ch) here: the read loop's response branch
		// reads ch under d.mu, then sends ch<-resp AFTER releasing the lock, so
		// closing ch would race that send into a send-on-closed-channel panic.
		// The buffered-size-1 channels are simply abandoned (the waiter has
		// already stopped reading) and garbage-collected.
		for id := range d.pending {
			delete(d.pending, id)
		}
		d.mu.Unlock()
		_ = d.writer.Close()
		close(d.stopped)
	})
	return nil
}

// readLoop runs on a dedicated goroutine. Exits on io.EOF, an unrecoverable
// read error, or a parse give-up, delivering the terminal error to LoopError
// and closing all outbound channels.
//
// Telemetry: OnFirstMessage fires on the first successfully decoded frame;
// OnParseError on each decode failure (carrying the consecutive count);
// OnParseGiveUp + onUnrecoverable when consecutive failures cross the
// threshold; OnBackpressure when an outbound channel saturates; OnUnknownMessage
// when a frame cannot be classified.
func (d *Demux) readLoop(ctx context.Context) {
	var exitErr error
	defer func() {
		d.loopErr <- exitErr
		close(d.notifications)
		close(d.serverRequests)
	}()

	loopStart := time.Now()
	firstMessage := true
	var consecutiveParseErrors uint
	gapQueued := false

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
			consecutiveParseErrors++
			continueLoop, nextGapQueued, decodeExitErr := d.handleDecodeFailure(
				ctx, line, err, consecutiveParseErrors, gapQueued,
			)
			if !continueLoop {
				exitErr = decodeExitErr
				return
			}
			gapQueued = nextGapQueued
			continue
		}

		// Successful decode — reset the give-up counter and surface first-token.
		consecutiveParseErrors = 0
		if firstMessage {
			firstMessage = false
			d.observer.OnFirstMessage(time.Since(loopStart))
		}

		continueLoop, nextGapQueued := d.classifyAndRoute(ctx, frame, line, gapQueued)
		if !continueLoop {
			return
		}
		gapQueued = nextGapQueued
	}
}

func (d *Demux) handleDecodeFailure(
	ctx context.Context,
	line []byte,
	decodeErr error,
	consecutive uint,
	gapQueued bool,
) (continueLoop, nextGapQueued bool, exitErr error) {
	// recordParseFailure receives the incremented value so telemetry reports the
	// running consecutive count.
	giveUpErr := d.recordParseFailure(line, consecutive, decodeErr)
	if !gapQueued {
		frameErr := fmt.Errorf("jsonrpc.Demux.readLoop: decode inbound frame: %w", decodeErr)
		// One marker covers a consecutive malformed-frame run. Coalescing keeps a
		// high give-up threshold from filling the queue before Connect starts its
		// dispatcher; the next classifiable frame remains FIFO behind the marker.
		if !d.deliverNotification(ctx, Notification{DecodeError: frameErr}) {
			return false, false, nil
		}
		gapQueued = true
	}
	if giveUpErr != nil {
		return false, gapQueued, giveUpErr
	}
	return true, gapQueued, nil
}

// recordParseFailure emits parse-error telemetry for a malformed inbound frame
// and decides whether the stream is unrecoverable. It returns a non-nil
// terminal error once consecutive failures reach maxParseErrors — after firing
// OnParseGiveUp and onUnrecoverable — so readLoop terminates; otherwise it
// returns nil and the loop continues. The caller increments consecutive before
// calling so the count is the running total.
func (d *Demux) recordParseFailure(line []byte, consecutive uint, err error) error {
	d.observer.OnParseError(consecutive, err)
	d.logger.Warn("jsonrpc.Demux: malformed inbound frame",
		zap.Error(err),
		zap.Uint("consecutive_errors", consecutive),
		zap.ByteString("line", truncate(line, 512)))

	// Give up after sustained garbage: the subprocess is unrecoverable.
	// Terminate it authoritatively (transport kills it) and surface a typed
	// terminal error rather than spinning forever or leaving a zombie.
	if consecutive < d.maxParseErrors {
		return nil
	}
	d.logger.Error("jsonrpc.Demux: too many consecutive decode errors, terminating subprocess",
		zap.Uint("consecutive_errors", consecutive))
	d.observer.OnParseGiveUp(consecutive)
	exitErr := fmt.Errorf("jsonrpc.Demux.readLoop: %d consecutive decode errors: %w", consecutive, ErrParseGiveUp)
	if d.onUnrecoverable != nil {
		d.onUnrecoverable(exitErr)
	}
	return exitErr
}

// classifyAndRoute dispatches a successfully decoded frame to the correct
// outbound channel: a server-initiated request, a notification, a response to a
// pending client request, or an unclassifiable frame (telemetry plus an ordered
// notification-stream gap). It
// The second result reports whether a coalesced frame-gap marker remains
// pending for the current malformed run. The first result is false only when
// readLoop must exit because an outbound delivery observed stop or cancellation.
func (d *Demux) classifyAndRoute(
	ctx context.Context,
	frame rawFrame,
	line []byte,
	gapQueued bool,
) (continueLoop, nextGapQueued bool) {
	switch {
	case frame.Method != nil && frame.ID != nil:
		// Server-initiated request.
		req := ServerRequest{ID: *frame.ID, Method: *frame.Method, Params: frame.Params}
		return d.deliverServerRequest(ctx, req), false

	case frame.Method != nil:
		// Notification.
		note := Notification{Method: *frame.Method, Params: frame.Params}
		return d.deliverNotification(ctx, note), false

	case frame.ID != nil:
		// Response to a client-initiated request.
		resp := Response{ID: *frame.ID, Result: frame.Result, Error: frame.Error}
		d.mu.Lock()
		ch, ok := d.pending[*frame.ID]
		d.mu.Unlock()
		if !ok {
			d.logger.Warn("jsonrpc.Demux: unsolicited response",
				zap.Uint64("id", *frame.ID))
			return true, false
		}
		// ch is buffered size 1; this never blocks.
		ch <- resp
		return true, false

	default:
		// Unclassifiable frame — no id, no method, no result. Either codex
		// wire-format drift or corruption. Surface telemetry and an ordered gap;
		// the JSON decoded, but it may have been a damaged notification.
		d.observer.OnUnknownMessage("unclassifiable-frame")
		d.logger.Warn("jsonrpc.Demux: unclassifiable frame",
			zap.ByteString("line", truncate(line, 512)))
		if gapQueued {
			return true, true
		}
		return d.deliverNotification(ctx, Notification{
			DecodeError: fmt.Errorf("jsonrpc.Demux.classifyAndRoute: %w", ErrUnclassifiableFrame),
		}), true
	}
}

// deliverNotification pushes a notification onto the outbound channel. It tries
// a non-blocking send first; if the channel is saturated (the dispatcher is
// behind), it emits OnBackpressure exactly once and then blocks until the send
// succeeds, the demux stops, or ctx is canceled. Returns false only when the
// loop must exit.
func (d *Demux) deliverNotification(ctx context.Context, note Notification) bool {
	select {
	case d.notifications <- note:
		return true
	case <-d.stopped:
		return false
	case <-ctx.Done():
		return false
	default:
		// Channel full — the consumer is behind. Surface backpressure, then block.
	}
	d.observer.OnBackpressure()
	select {
	case d.notifications <- note:
		return true
	case <-d.stopped:
		return false
	case <-ctx.Done():
		return false
	}
}

// deliverServerRequest is the server-request analog of deliverNotification.
func (d *Demux) deliverServerRequest(ctx context.Context, req ServerRequest) bool {
	select {
	case d.serverRequests <- req:
		return true
	case <-d.stopped:
		return false
	case <-ctx.Done():
		return false
	default:
		// Channel full — the consumer is behind. Surface backpressure, then block.
	}
	d.observer.OnBackpressure()
	select {
	case d.serverRequests <- req:
		return true
	case <-d.stopped:
		return false
	case <-ctx.Done():
		return false
	}
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
