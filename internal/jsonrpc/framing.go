package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// MinReadBufferSize is the minimum buffer size for the line-reading scanner.
// 2 MiB: user input cap is 1 MiB and envelope overhead pushes some
// notifications past that (e.g., long command_execution output chunks).
const MinReadBufferSize = 2 * 1024 * 1024

// ErrFramePoisoned means the write side can no longer carry intact frames.
var ErrFramePoisoned = errors.New("jsonrpc: unusable frame writer")

// LineWriter owns one write slot for subprocess stdin. Admission to the slot
// is cancellable and no other frame can interleave with a running write.
type LineWriter struct {
	slot      chan struct{}
	w         io.Writer
	closer    io.Closer
	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

// NewLineWriter wraps w. A blocking writer must support write deadlines or
// Close must unblock its Write method, as it does for subprocess stdin.
func NewLineWriter(w io.Writer) *LineWriter {
	lw := &LineWriter{w: w, slot: make(chan struct{}, 1), closed: make(chan struct{})}
	if closer, ok := w.(io.Closer); ok {
		lw.closer = closer
	}
	return lw
}

func (lw *LineWriter) WriteFrame(v any) error {
	return lw.WriteFrameContext(context.Background(), v)
}

// WriteFrameContext returns nil if the whole frame was written, even if
// cancellation raced with completion. Cancellation before writing any bytes
// leaves a deadline-capable pipe usable. Partial frames are never reusable.
func (lw *LineWriter) WriteFrameContext(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("jsonrpc.LineWriter.WriteFrame: marshal: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lw.closed:
		return ErrFramePoisoned
	case lw.slot <- struct{}{}:
	}
	defer func() { <-lw.slot }()
	select {
	case <-lw.closed:
		return ErrFramePoisoned
	default:
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	written, aborted, err := lw.writeContext(ctx, append(data, '\n'))
	if err == nil && !aborted {
		return nil
	}
	if ctx.Err() != nil && written == 0 && !aborted {
		return ctx.Err()
	}
	_ = lw.Close()
	return fmt.Errorf("jsonrpc.LineWriter.WriteFrame: %w: %w", ErrFramePoisoned, errors.Join(ctx.Err(), err))
}

func (lw *LineWriter) writeContext(ctx context.Context, data []byte) (written int, aborted bool, err error) {
	// Some native pipes report progress directly with a nonblocking write.
	if writer, ok := lw.w.(interface {
		WriteContext(context.Context, []byte) (int, error)
	}); ok {
		n, writeErr := writer.WriteContext(ctx, data)
		return n, false, writeErr
	}
	deadline, _ := lw.w.(interface{ SetWriteDeadline(time.Time) error })
	interrupted := make(chan bool, 1)
	stop := context.AfterFunc(ctx, func() {
		if deadline != nil && deadline.SetWriteDeadline(time.Now()) == nil {
			interrupted <- false
			return
		}
		// Windows anonymous pipes and in-memory pipes do not necessarily
		// support deadlines. Close interrupts a blocked native pipe write.
		_ = lw.Close()
		interrupted <- true
	})
	defer func() {
		if !stop() {
			aborted = <-interrupted
			if !aborted {
				if clearErr := deadline.SetWriteDeadline(time.Time{}); clearErr != nil {
					aborted = true
					err = errors.Join(err, clearErr)
				}
			}
		}
	}()

	for written < len(data) {
		n, writeErr := lw.w.Write(data[written:])
		written += n
		if writeErr != nil {
			return written, false, writeErr
		}
		if n == 0 {
			return written, false, io.ErrShortWrite
		}
	}
	return written, false, nil
}

// Close closes the underlying writer when it is closeable. It intentionally does
// not take the write mutex: Close must be able to unblock a goroutine currently
// stuck inside the underlying Write call during shutdown.
func (lw *LineWriter) Close() error {
	if lw == nil {
		return nil
	}
	lw.closeOnce.Do(func() {
		close(lw.closed)
		if lw.closer != nil {
			lw.closeErr = lw.closer.Close()
		}
	})
	return lw.closeErr
}

// LineReader reads newline-terminated JSON frames from the underlying reader
// using a bufio.Scanner with a 2 MiB buffer ceiling.
type LineReader struct {
	scanner *bufio.Scanner
}

// NewLineReader wraps r with a 2 MiB buffer. Use NewLineReaderWithSize to
// override.
func NewLineReader(r io.Reader) *LineReader {
	return NewLineReaderWithSize(r, MinReadBufferSize)
}

// NewLineReaderWithSize wraps r with a custom max buffer size. Sizes below
// MinReadBufferSize are raised to the minimum.
func NewLineReaderWithSize(r io.Reader, maxSize int) *LineReader {
	if maxSize < MinReadBufferSize {
		maxSize = MinReadBufferSize
	}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), maxSize)
	return &LineReader{scanner: s}
}

// ReadLine returns the next complete line without its trailing newline.
// Returns io.EOF when the stream ends cleanly.
func (lr *LineReader) ReadLine() ([]byte, error) {
	if !lr.scanner.Scan() {
		if err := lr.scanner.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				return nil, fmt.Errorf("jsonrpc.LineReader.ReadLine: line exceeded %d bytes: %w", MinReadBufferSize, err)
			}
			return nil, fmt.Errorf("jsonrpc.LineReader.ReadLine: %w", err)
		}
		return nil, io.EOF
	}
	// Copy because scanner reuses its internal buffer.
	src := lr.scanner.Bytes()
	out := make([]byte, len(src))
	copy(out, src)
	return out, nil
}
