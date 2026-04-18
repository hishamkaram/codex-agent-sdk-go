package jsonrpc

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestLineWriter_WriteFrameAppendsLF(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lw := NewLineWriter(&buf)
	if err := lw.WriteFrame(Request{ID: 1, Method: "ping"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing LF, got %q", out)
	}
	// The marshaled request must contain id and method but NOT jsonrpc key.
	if !strings.Contains(out, `"id":1`) || !strings.Contains(out, `"method":"ping"`) {
		t.Fatalf("missing required fields in %q", out)
	}
	if strings.Contains(out, "jsonrpc") {
		t.Fatalf("jsonrpc field must be omitted on wire, got %q", out)
	}
}

// Write from many goroutines concurrently — verify no frame is interleaved
// (each line is valid JSON and contains both opening and closing braces).
func TestLineWriter_ConcurrentFramesNeverInterleave(t *testing.T) {
	t.Parallel()
	var buf safeBuffer
	lw := NewLineWriter(&buf)

	const goroutines = 32
	const perGoroutine = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = lw.WriteFrame(Request{ID: uint64(g*perGoroutine + i + 1), Method: "x"})
			}
		}(g)
	}
	wg.Wait()

	total := goroutines * perGoroutine
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != total {
		t.Fatalf("got %d lines, want %d", len(lines), total)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Fatalf("line %d not a complete JSON object: %q", i, line)
		}
	}
}

func TestLineReader_ReadsTwoFrames(t *testing.T) {
	t.Parallel()
	src := strings.NewReader(`{"method":"a"}` + "\n" + `{"method":"b","params":{"k":1}}` + "\n")
	lr := NewLineReader(src)
	line1, err := lr.ReadLine()
	if err != nil || string(line1) != `{"method":"a"}` {
		t.Fatalf("line1: err=%v line=%q", err, line1)
	}
	line2, err := lr.ReadLine()
	if err != nil || string(line2) != `{"method":"b","params":{"k":1}}` {
		t.Fatalf("line2: err=%v line=%q", err, line2)
	}
	if _, err := lr.ReadLine(); err != io.EOF {
		t.Fatalf("expected io.EOF on third read, got %v", err)
	}
}

func TestLineReader_HandlesLargeFrame(t *testing.T) {
	t.Parallel()
	// 1.5 MiB frame — above the old 64 KiB default but within the 2 MiB cap.
	payload := strings.Repeat("A", 1500*1024)
	line := `{"method":"big","params":{"blob":"` + payload + `"}}`
	src := strings.NewReader(line + "\n")
	lr := NewLineReader(src)
	got, err := lr.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(line) {
		t.Fatalf("truncated: got %d bytes, want %d", len(got), len(line))
	}
}

func TestLineReader_RejectsOversizedFrame(t *testing.T) {
	t.Parallel()
	// Make a frame just above the 2 MiB ceiling.
	payload := strings.Repeat("A", MinReadBufferSize+1024)
	src := strings.NewReader(payload + "\n")
	lr := NewLineReader(src)
	_, err := lr.ReadLine()
	if err == nil {
		t.Fatal("expected error for oversized frame, got nil")
	}
}

func TestNewLineReaderWithSize_RaisesBelowMinimum(t *testing.T) {
	t.Parallel()
	// Request 1 KiB cap; reader must silently raise to 2 MiB so a 1.5 MiB
	// frame still succeeds.
	payload := strings.Repeat("A", 1500*1024)
	src := strings.NewReader(payload + "\n")
	lr := NewLineReaderWithSize(src, 1024)
	got, err := lr.ReadLine()
	if err != nil {
		t.Fatalf("expected raise-to-min; got error: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("got %d bytes, want %d", len(got), len(payload))
	}
}

// safeBuffer adds a mutex so concurrent writers in tests don't race the
// bytes.Buffer (which is not safe for concurrent Write).
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
