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
