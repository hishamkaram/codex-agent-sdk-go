package jsonrpc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// TestDemux_ConcurrentCloseAndResponseNoPanic stresses the race between
// Close() and the read loop's response delivery. The read loop reads the
// pending channel under d.mu, releases the lock, then sends ch<-resp; if
// Close() closed that channel in the window, the send panics with
// "send on closed channel". The fix makes Close() stop closing pending
// channels (waiters are released via d.stopped instead), so the send can
// never hit a closed channel. A panic here fails the whole test binary.
func TestDemux_ConcurrentCloseAndResponseNoPanic(t *testing.T) {
	t.Parallel()

	const iterations = 60
	const inflight = 16

	for iter := 0; iter < iterations; iter++ {
		d, s := makeDemux(t)

		// Server echoes a response for every request it receives, so responses
		// are in-flight exactly while Close() fires.
		go func() {
			for raw := range s.received {
				var req Request
				if json.Unmarshal(raw, &req) != nil {
					continue
				}
				// Best-effort: the pipe may already be torn down by s.close().
				_, _ = s.serverOut.Write([]byte(`{"id":` + itoa(int(req.ID)) + `,"result":{}}` + "\n"))
			}
		}()

		var wg sync.WaitGroup
		for i := 0; i < inflight; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				// Result is irrelevant — we only care that no send-on-closed
				// panic occurs as Close() races the response delivery.
				_, _ = d.Send(ctx, "ping", nil)
			}()
		}

		// Close concurrently with the in-flight responses.
		go func() { _ = d.Close() }()

		wg.Wait()
		_ = d.Close()
		s.close()
	}
}
