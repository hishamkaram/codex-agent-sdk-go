package codex

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
	if c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: %w", types.ErrClientClosed)
	}
	if !c.connected.Load() {
		return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: %w", types.ErrClientNotConnected)
	}

	subscriber := newThreadEventSubscriber(buffer)
	c.mu.Lock()
	if c.closed.Load() || !c.connected.Load() {
		c.mu.Unlock()
		subscriber.close()
		if c.closed.Load() {
			return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: %w", types.ErrClientClosed)
		}
		return nil, fmt.Errorf("codex.Client.SubscribeThreadEvents: %w", types.ErrClientNotConnected)
	}
	if c.threadEventSubscribers == nil {
		c.threadEventSubscribers = make(map[uint64]*threadEventSubscriber)
	}
	c.nextThreadEventSubscriberID++
	id := c.nextThreadEventSubscriberID
	c.threadEventSubscribers[id] = subscriber
	c.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			c.removeThreadEventSubscriber(id, subscriber)
		case <-subscriber.done:
		}
	}()
	return subscriber.events, nil
}

func (c *Client) publishThreadEvent(event ThreadEventEnvelope) {
	c.mu.Lock()
	subscribers := make(map[uint64]*threadEventSubscriber, len(c.threadEventSubscribers))
	for id, subscriber := range c.threadEventSubscribers {
		subscribers[id] = subscriber
	}
	c.mu.Unlock()

	for id, subscriber := range subscribers {
		if !subscriber.deliver(event) {
			c.removeThreadEventSubscriber(id, subscriber)
		}
	}
}

func (c *Client) failThreadEventSubscriptions(event ThreadEventEnvelope) {
	c.mu.Lock()
	subscribers := make(map[uint64]*threadEventSubscriber, len(c.threadEventSubscribers))
	for id, subscriber := range c.threadEventSubscribers {
		subscribers[id] = subscriber
	}
	c.mu.Unlock()

	for id, subscriber := range subscribers {
		if subscriber.fail(event) {
			c.removeThreadEventSubscriber(id, subscriber)
		}
	}
}

func (c *Client) closeThreadEventSubscriptions() {
	c.mu.Lock()
	subscribers := c.threadEventSubscribers
	c.threadEventSubscribers = nil
	c.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.close()
	}
}

func (c *Client) removeThreadEventSubscriber(id uint64, subscriber *threadEventSubscriber) {
	c.mu.Lock()
	if current := c.threadEventSubscribers[id]; current == subscriber {
		delete(c.threadEventSubscribers, id)
	}
	c.mu.Unlock()
	subscriber.close()
}
