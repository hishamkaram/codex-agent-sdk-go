package codex

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/session"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const maxThreadEventSubscriptionBuffer = 4096

// ErrThreadEventStreamGap marks a subscription that is no longer complete,
// because its buffer overflowed, an inbound frame or notification could not be
// parsed, or the provider event source ended unexpectedly.
// The stream closes after delivering the gap so a caller cannot mistake later
// output for a complete transcript.
var ErrThreadEventStreamGap = errors.New("codex thread event stream gap")

// ThreadEventEnvelope retains the provider thread identity alongside one
// parsed event. Err is non-nil only for a terminal stream gap record.
type ThreadEventEnvelope struct {
	ThreadID string
	Event    types.ThreadEvent
	Err      error
}

// ThreadEventStreamGapError reports where a bounded subscription overflowed.
type ThreadEventStreamGapError struct {
	ThreadID string
	Buffer   int
}

func (e *ThreadEventStreamGapError) Error() string {
	return fmt.Sprintf("%v: buffer %d exhausted while delivering thread %q", ErrThreadEventStreamGap, e.Buffer, e.ThreadID)
}

func (e *ThreadEventStreamGapError) Unwrap() error { return ErrThreadEventStreamGap }

// ThreadEventParseGapError reports a notification the SDK could not parse.
// The client-wide stream closes after this record because subsequent output
// cannot restore transcript completeness.
type ThreadEventParseGapError struct {
	ThreadID string
	Method   string
	Cause    error
}

func (e *ThreadEventParseGapError) Error() string {
	return fmt.Sprintf("%v: parse %q for thread %q: %v", ErrThreadEventStreamGap, e.Method, e.ThreadID, e.Cause)
}

func (e *ThreadEventParseGapError) Unwrap() []error {
	return []error{ErrThreadEventStreamGap, e.Cause}
}

// ThreadEventFrameGapError reports an inbound app-server frame that the SDK
// could not decode. The frame cannot be attributed to a thread, so every active
// client-wide subscription closes after this record rather than presenting
// later output as complete.
type ThreadEventFrameGapError struct {
	Cause error
}

func (e *ThreadEventFrameGapError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%v: decode inbound app-server frame", ErrThreadEventStreamGap)
	}
	return fmt.Sprintf("%v: decode inbound app-server frame: %v", ErrThreadEventStreamGap, e.Cause)
}

func (e *ThreadEventFrameGapError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrThreadEventStreamGap}
	}
	return []error{ErrThreadEventStreamGap, e.Cause}
}

// ThreadEventSourceGapError reports that the app-server event source ended
// without an intentional client shutdown. Any Cause is the terminal demux
// error; a clean provider EOF has no cause but is still a stream gap.
type ThreadEventSourceGapError struct {
	Cause error
}

func (e *ThreadEventSourceGapError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%v: app-server event source closed", ErrThreadEventStreamGap)
	}
	return fmt.Sprintf("%v: app-server event source closed: %v", ErrThreadEventStreamGap, e.Cause)
}

func (e *ThreadEventSourceGapError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrThreadEventStreamGap}
	}
	return []error{ErrThreadEventStreamGap, e.Cause}
}

type threadEventSubscriber struct {
	mu            sync.Mutex
	events        chan ThreadEventEnvelope
	done          chan struct{}
	eventCapacity int
	closed        bool
}

func newThreadEventSubscriber(buffer int) *threadEventSubscriber {
	return &threadEventSubscriber{
		// The reserved slot guarantees that overflow can be reported without
		// blocking the app-server dispatcher.
		events:        make(chan ThreadEventEnvelope, buffer+1),
		done:          make(chan struct{}),
		eventCapacity: buffer,
	}
}

func (s *threadEventSubscriber) deliver(event ThreadEventEnvelope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if len(s.events) >= s.eventCapacity {
		s.events <- ThreadEventEnvelope{
			ThreadID: event.ThreadID,
			Err: &ThreadEventStreamGapError{
				ThreadID: event.ThreadID,
				Buffer:   s.eventCapacity,
			},
		}
		s.closed = true
		close(s.events)
		close(s.done)
		return false
	}
	s.events <- event
	return true
}

func (s *threadEventSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.events)
	close(s.done)
}

func (s *threadEventSubscriber) fail(event ThreadEventEnvelope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	// One slot is reserved beyond eventCapacity for this terminal record.
	s.events <- event
	s.closed = true
	close(s.events)
	close(s.done)
	return true
}

// SubscribeThreadEvents streams parsed notifications for every provider
// thread, including child threads that have no SDK Thread handle. Delivery is
// FIFO. The context or Client.Close ends the subscription cleanly; unexpected
// provider event-source closure emits a terminal gap before closing.
func (c *Client) SubscribeThreadEvents(ctx context.Context, buffer int) (<-chan ThreadEventEnvelope, error) {
	if ctx == nil {
		return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: context is required")
	}
	if buffer < 1 || buffer > maxThreadEventSubscriptionBuffer {
		return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: buffer must be between 1 and %d", maxThreadEventSubscriptionBuffer)
	}
	subscriber := newThreadEventSubscriber(buffer)
	var id uint64
	if err := c.session.WithReady(func() {
		id = c.subscriptions.add(subscriber)
	}); err != nil {
		subscriber.close()
		return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: %w", err)
	}

	go func() {
		select {
		case <-ctx.Done():
			c.subscriptions.remove(id, subscriber)
		case <-subscriber.done:
		}
	}()
	return subscriber.events, nil
}

// threadSubscriptions owns the concrete public queues. Enrollment runs
// under Session's readiness lock, then under this registry's own lock.
type threadSubscriptions struct {
	mu      sync.Mutex
	entries map[uint64]*threadEventSubscriber
	nextID  uint64
}

func (s *threadSubscriptions) add(subscriber *threadEventSubscriber) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[uint64]*threadEventSubscriber)
	}
	s.nextID++
	s.entries[s.nextID] = subscriber
	return s.nextID
}

func (s *threadSubscriptions) snapshot() map[uint64]*threadEventSubscriber {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.entries)
}

func (s *threadSubscriptions) Publish(threadID string, event types.ThreadEvent) {
	envelope := ThreadEventEnvelope{ThreadID: threadID, Event: event}
	for id, subscriber := range s.snapshot() {
		if !subscriber.deliver(envelope) {
			s.remove(id, subscriber)
		}
	}
}

func (s *threadSubscriptions) Fail(gap session.Gap) {
	event := ThreadEventEnvelope{ThreadID: gap.ThreadID}
	switch gap.Kind {
	case session.FrameGap:
		event.Err = &ThreadEventFrameGapError{Cause: gap.Cause}
	case session.ParseGap:
		event.Err = &ThreadEventParseGapError{ThreadID: gap.ThreadID, Method: gap.Method, Cause: gap.Cause}
	case session.SourceGap:
		event.Err = &ThreadEventSourceGapError{Cause: gap.Cause}
	}
	for id, subscriber := range s.snapshot() {
		if subscriber.fail(event) {
			s.remove(id, subscriber)
		}
	}
}

func (s *threadSubscriptions) Close() {
	s.mu.Lock()
	subscribers := s.entries
	s.entries = nil
	s.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.close()
	}
}

func (s *threadSubscriptions) remove(id uint64, subscriber *threadEventSubscriber) {
	s.mu.Lock()
	if current := s.entries[id]; current == subscriber {
		delete(s.entries, id)
	}
	s.mu.Unlock()
	subscriber.close()
}
