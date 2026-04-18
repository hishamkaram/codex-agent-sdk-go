package transport

import (
	"context"
	"sync"
)

// WarmPool is a v0.1.0 stub. It will eventually pre-spawn `codex app-server`
// processes so the first Query() of a program avoids a ~200-400 ms cold
// start. For now the pool has capacity 0 — every caller spawns its own
// subprocess on demand.
//
// The pool is exposed so the root package's Startup() option has a stable
// API, and so tests can drop in a preloaded pool.
type WarmPool struct {
	mu sync.Mutex
	// Reserved for future: slice of idle *AppServer ready to be consumed.
}

// NewWarmPool returns an empty pool.
func NewWarmPool() *WarmPool { return &WarmPool{} }

// Acquire returns a ready AppServer or nil if the pool is empty (v0.1.0
// always returns nil; callers fall through to NewAppServer + Connect).
func (p *WarmPool) Acquire(ctx context.Context) *AppServer {
	_ = ctx
	p.mu.Lock()
	defer p.mu.Unlock()
	return nil
}

// Release returns an AppServer to the pool for reuse. v0.1.0 discards it
// (caller should Close it instead).
func (p *WarmPool) Release(t *AppServer) {
	_ = t
}
