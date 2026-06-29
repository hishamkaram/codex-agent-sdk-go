package transport

import (
	"bytes"
	"sync"
)

// ringBuffer is a bounded byte buffer that keeps the most recent N bytes.
// Safe for concurrent Write from one goroutine and String from another.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	size int
	full bool
	pos  int // next write index when not full; write head when full
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{data: make([]byte, 0, size), size: size}
}

// Write implements io.Writer. Always returns (len(p), nil).
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	written := len(p)

	if !r.full {
		room := r.size - len(r.data)
		if len(p) <= room {
			r.data = append(r.data, p...)
			if len(r.data) == r.size {
				r.full = true
				r.pos = 0
			}
			return written, nil
		}
		r.data = append(r.data, p[:room]...)
		p = p[room:]
		r.full = true
		r.pos = 0
		// Fall through to the ring-mode path.
	}

	// Full-mode: write each byte at r.pos and advance.
	if len(p) >= r.size {
		p = p[len(p)-r.size:]
		copy(r.data, p)
		r.pos = 0
		return written, nil
	}
	n := copy(r.data[r.pos:], p)
	if n < len(p) {
		copy(r.data, p[n:])
		r.pos = len(p) - n
	} else {
		r.pos += n
		if r.pos == r.size {
			r.pos = 0
		}
	}
	return written, nil
}

// String returns the buffered bytes in chronological order.
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return string(r.data)
	}
	var out bytes.Buffer
	out.Grow(r.size)
	out.Write(r.data[r.pos:])
	out.Write(r.data[:r.pos])
	return out.String()
}
