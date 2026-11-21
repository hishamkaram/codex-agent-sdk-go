package jsonrpc

import "context"

func (d *Demux) deliverServerMessage(ctx context.Context, message ServerMessage) bool {
	select {
	case d.serverMessages <- message:
		return true
	case <-d.stopped:
		return false
	case <-ctx.Done():
		return false
	default:
	}
	d.observer.OnBackpressure()
	select {
	case d.serverMessages <- message:
		return true
	case <-d.stopped:
		return false
	case <-ctx.Done():
		return false
	}
}

func (d *Demux) deliverOrderedNotification(ctx context.Context, notification *Notification) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	// At most the bounded ordered channel, one message being applied, and
	// this read loop's in-flight delivery can hold notification receipts.
	d.pendingNotifications[notification] = struct{}{}
	if notification.DecodeError == nil {
		d.malformedRun = false
	}
	d.mu.Unlock()
	if d.deliverServerMessage(ctx, ServerMessage{Notification: notification}) {
		return true
	}
	d.NotificationApplied(notification)
	return false
}

// NotificationApplied releases a receipt after ordered application.
func (d *Demux) NotificationApplied(notification *Notification) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pendingNotifications, notification)
}

// CheckPendingNotification checks shutdown and unapplied receipts under one
// lock. It reports a malformed run or a notification matching a pure
// predicate. The predicate must not call the demux or mutate the notification.
func (d *Demux) CheckPendingNotification(match func(Notification) bool) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false, ErrClosed
	}
	if d.malformedRun {
		return true, nil
	}
	for notification := range d.pendingNotifications {
		if match(*notification) {
			return true, nil
		}
	}
	return false, nil
}

func (d *Demux) setMalformedRun(active bool) {
	if d.serverMessages == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.malformedRun = active
	}
}
