package jsonrpc

import (
	"sync"
	"testing"
)

func TestIDAllocator_StartsAtOne(t *testing.T) {
	t.Parallel()
	var a IDAllocator
	if got := a.Next(); got != 1 {
		t.Fatalf("Next() first call = %d, want 1", got)
	}
	if got := a.Next(); got != 2 {
		t.Fatalf("Next() second call = %d, want 2", got)
	}
}

func TestIDAllocator_Concurrent(t *testing.T) {
	t.Parallel()
	var a IDAllocator
	const goroutines = 64
	const perGoroutine = 100

	seen := sync.Map{}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id := a.Next()
				if _, dup := seen.LoadOrStore(id, true); dup {
					t.Errorf("duplicate id %d", id)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Verify IDs span 1..goroutines*perGoroutine exactly.
	total := uint64(goroutines * perGoroutine)
	for i := uint64(1); i <= total; i++ {
		if _, ok := seen.Load(i); !ok {
			t.Fatalf("missing id %d", i)
		}
	}
}
