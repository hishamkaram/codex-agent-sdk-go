package jsonrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MinReadBufferSize is the minimum buffer size for the line-reading scanner.
// 2 MiB: user input cap is 1 MiB and envelope overhead pushes some
// notifications past that (e.g., long command_execution output chunks).
const MinReadBufferSize = 2 * 1024 * 1024

// LineWriter serializes all writes to the underlying writer with a single
// mutex. This is the single point that enforces the "frames may never
// interleave on stdin" invariant documented in CLAUDE.md.
type LineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLineWriter wraps w. The writer is not buffered; every WriteLine call
// writes the full frame atomically.
func NewLineWriter(w io.Writer) *LineWriter {
	return &LineWriter{w: w}
}

// WriteFrame marshals v as JSON, appends a newline, and writes the result
// in one Write call holding the mutex. v MUST marshal to a valid JSON-RPC
// frame (Request | Notification | Response).
func (lw *LineWriter) WriteFrame(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("jsonrpc.LineWriter.WriteFrame: marshal: %w", err)
	}
	// Append \n in the same buffer so os.Pipe sees one Write. io.Pipe on a
	// subprocess stdin also honors this; short-writes are still possible on
	// some OSes, so loop defensively.
	line := append(data, '\n')
	lw.mu.Lock()
	defer lw.mu.Unlock()
	for len(line) > 0 {
		n, werr := lw.w.Write(line)
		if werr != nil {
			return fmt.Errorf("jsonrpc.LineWriter.WriteFrame: write: %w", werr)
		}
		line = line[n:]
	}
	return nil
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
