package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type receiptBarrierObserver struct {
	types.NopObserver
	entered chan string
	release chan struct{}
}

func (o *receiptBarrierObserver) OnParseError(uint, error) {
	o.entered <- "decode"
	<-o.release
}

func (o *receiptBarrierObserver) OnUnknownMessage(string) {
	o.entered <- "classification"
	<-o.release
}

func TestDemux_MalformedRunGuardsBeforeGapDeliveryAndThroughRetirement(t *testing.T) {
	t.Parallel()
	observer := &receiptBarrierObserver{
		entered: make(chan string, 2),
		release: make(chan struct{}),
	}
	d, s := makeDemuxWithObserver(t, WithObserver(observer), WithOrderedServerMessages())
	defer func() {
		close(observer.release)
		_ = d.Close()
		s.close()
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	assertPending := func(want bool, stage string) {
		t.Helper()
		result := make(chan bool, 1)
		go func() {
			result <- d.HasPendingNotification(func(note Notification) bool {
				return note.DecodeError != nil || note.Method == "turn/started"
			})
		}()
		if got := awaitReceiptTestValue(t, ctx, result, stage); got != want {
			t.Fatalf("%s: guard = %t, want %t", stage, got, want)
		}
	}

	s.sendToClient(t, `{"method":`)
	if got := awaitReceiptTestValue(t, ctx, observer.entered, "decode observer"); got != "decode" {
		t.Fatalf("observer = %q, want decode", got)
	}
	select {
	case message := <-d.ServerMessages():
		t.Fatalf("gap delivered before observer returned: %+v", message)
	default:
	}
	assertPending(true, "before the first gap marker can enqueue")
	observer.release <- struct{}{}

	first := awaitReceiptTestValue(t, ctx, d.ServerMessages(), "first gap marker")
	if first.Notification == nil || first.Notification.DecodeError == nil {
		t.Fatalf("first message = %+v, want decode gap", first)
	}
	d.NotificationApplied(first.Notification)
	assertPending(true, "after the first gap marker is applied")

	s.sendToClient(t, `{}`)
	if got := awaitReceiptTestValue(t, ctx, observer.entered, "unclassifiable frame observer"); got != "classification" {
		t.Fatalf("observer = %q, want classification", got)
	}
	assertPending(true, "second malformed frame before telemetry returns")
	observer.release <- struct{}{}

	s.sendToClient(t, `{"method":"turn/started","params":{"threadId":"t1","turn":{"id":"turn-1"}}}`)
	next := awaitReceiptTestValue(t, ctx, d.ServerMessages(), "first valid notification")
	if next.Notification == nil || next.Notification.DecodeError != nil || next.Notification.Method != "turn/started" {
		t.Fatalf("next message = %+v, want turn/started without a redundant gap marker", next)
	}
	assertPending(true, "while the valid notification is awaiting application")
	d.NotificationApplied(next.Notification)
	assertPending(false, "after valid notification application ends the gap")
}

func awaitReceiptTestValue[T any](t *testing.T, ctx context.Context, source <-chan T, stage string) T {
	t.Helper()
	select {
	case value, ok := <-source:
		if !ok {
			t.Fatalf("%s: channel closed", stage)
		}
		return value
	case <-ctx.Done():
		t.Fatalf("%s: %v", stage, ctx.Err())
	}
	var zero T
	return zero
}
