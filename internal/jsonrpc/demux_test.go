package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockServer simulates a codex app-server: it reads client frames, can send
// arbitrary replies/notifications/server-requests, and exposes a channel of
// received client frames for assertions.
type mockServer struct {
	clientIn  *io.PipeReader // client writes → server reads
	clientOut *io.PipeWriter
	serverIn  *io.PipeReader // server writes → client reads
	serverOut *io.PipeWriter

	received chan json.RawMessage
}

func newMockServer() *mockServer {
	ciR, ciW := io.Pipe()
	soR, soW := io.Pipe()
	s := &mockServer{
		clientIn:  ciR,
		clientOut: ciW,
		serverIn:  soR,
		serverOut: soW,
		received:  make(chan json.RawMessage, 32),
	}
	// Reader goroutine for client→server frames.
	go func() {
		lr := NewLineReader(ciR)
		for {
			line, err := lr.ReadLine()
			if err != nil {
				close(s.received)
				return
			}
			cp := make([]byte, len(line))
			copy(cp, line)
			s.received <- cp
		}
	}()
	return s
}

// sendToClient writes a raw JSON frame to the client.
func (s *mockServer) sendToClient(t *testing.T, raw string) {
	t.Helper()
	if _, err := s.serverOut.Write([]byte(raw + "\n")); err != nil {
		t.Fatal(err)
	}
}

// close tears down both pipes so the client's readLoop exits.
func (s *mockServer) close() {
	_ = s.serverOut.Close()
	_ = s.clientIn.Close()
}

// makeDemux wires a Demux to a mockServer's client-facing pipes.
func makeDemux(t *testing.T) (*Demux, *mockServer) {
	t.Helper()
	s := newMockServer()
	lr := NewLineReader(s.serverIn)
	lw := NewLineWriter(s.clientOut)
	d := NewDemux(lr, lw, nil)
	d.Run(context.Background())
	return d, s
}

func TestDemux_SendReceivesResponse(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	defer func() {
		_ = d.Close()
		s.close()
	}()

	// Server auto-replies once we see a request.
	go func() {
		raw := <-s.received
		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("unmarshal: %v", err)
			return
		}
		s.sendToClient(t, `{"id":`+itoa(int(req.ID))+`,"result":{"ok":true}}`)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := d.Send(ctx, "thread/start", map[string]string{"cwd": "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != 1 {
		t.Fatalf("resp.ID = %d, want 1", resp.ID)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Fatalf("result = %q, want %q", resp.Result, `{"ok":true}`)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestDemux_SendReceivesRPCError(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	defer func() {
		_ = d.Close()
		s.close()
	}()

	go func() {
		raw := <-s.received
		var req Request
		_ = json.Unmarshal(raw, &req)
		s.sendToClient(t, `{"id":`+itoa(int(req.ID))+`,"error":{"code":-32601,"message":"method not found"}}`)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := d.Send(ctx, "bogus/method", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected resp.Error, got nil")
	}
	if resp.Error.Code != -32601 || resp.Error.Message != "method not found" {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestDemux_DeliversNotifications(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	defer func() {
		_ = d.Close()
		s.close()
	}()

	s.sendToClient(t, `{"method":"turn/started","params":{"turn":{"id":"t-1"}}}`)
	s.sendToClient(t, `{"method":"turn/completed","params":{"status":"success"}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	note1 := expectNotification(t, ctx, d)
	if note1.Method != "turn/started" {
		t.Fatalf("first note method = %q", note1.Method)
	}
	note2 := expectNotification(t, ctx, d)
	if note2.Method != "turn/completed" {
		t.Fatalf("second note method = %q", note2.Method)
	}
}

func TestDemux_DeliversServerRequestAndCallerResponds(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	defer func() {
		_ = d.Close()
		s.close()
	}()

	// Server sends an approval request with id=99.
	s.sendToClient(t, `{"id":99,"method":"item/commandExecution/requestApproval","params":{"command":"rm -rf /"}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	select {
	case req := <-d.ServerRequests():
		if req.ID != 99 || req.Method != "item/commandExecution/requestApproval" {
			t.Fatalf("unexpected server request: %+v", req)
		}
		// Respond with decision=decline.
		if err := d.RespondServerRequest(req.ID, map[string]string{"decision": "decline"}, nil); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("no server request received within 2s")
	}

	// The response we sent must land on the server's received channel.
	select {
	case raw := <-s.received:
		var r Response
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		if r.ID != 99 {
			t.Fatalf("response id = %d, want 99", r.ID)
		}
		if !strings.Contains(string(r.Result), `"decline"`) {
			t.Fatalf("response result missing decline: %q", r.Result)
		}
	case <-ctx.Done():
		t.Fatal("server didn't receive our response within 2s")
	}
}

func TestDemux_ClassifiesFrames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		wire        string
		wantNote    bool
		wantResp    bool // response to id=1 sent by this test
		wantServReq bool
	}{
		{
			name:     "notification: method only",
			wire:     `{"method":"turn/started","params":{}}`,
			wantNote: true,
		},
		{
			name:        "server-request: id + method",
			wire:        `{"id":77,"method":"item/permissions/requestApproval","params":{}}`,
			wantServReq: true,
		},
		{
			name:     "response: id + result, no method",
			wire:     `{"id":1,"result":{"thread":{"id":"T"}}}`,
			wantResp: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, s := makeDemux(t)
			defer func() {
				_ = d.Close()
				s.close()
			}()

			if tt.wantResp {
				// Kick off a Send for id=1 in background; it will complete
				// once the response below is injected.
				done := make(chan struct{})
				go func() {
					defer close(done)
					// Drain the outgoing request so the mockServer pipe doesn't block.
					<-s.received
					s.sendToClient(t, tt.wire)
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				resp, err := d.Send(ctx, "noop", nil)
				if err != nil {
					t.Fatal(err)
				}
				if resp.ID != 1 {
					t.Fatalf("resp.ID = %d", resp.ID)
				}
				<-done
				return
			}

			s.sendToClient(t, tt.wire)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			if tt.wantNote {
				n := expectNotification(t, ctx, d)
				if n.Method == "" {
					t.Fatal("empty method")
				}
			}
			if tt.wantServReq {
				select {
				case req := <-d.ServerRequests():
					if req.ID == 0 || req.Method == "" {
						t.Fatalf("malformed server-request: %+v", req)
					}
				case <-ctx.Done():
					t.Fatal("no server request within timeout")
				}
			}
		})
	}
}

func TestDemux_SendCancelsOnContextCancel(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	defer func() {
		_ = d.Close()
		s.close()
	}()

	// Drain the outgoing frame so the pipe doesn't block.
	go func() { <-s.received }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := d.Send(ctx, "will_timeout", nil)
	if err == nil {
		t.Fatal("expected ctx-timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestDemux_SendFailsAfterClose(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	_ = d.Close()
	s.close()

	_, err := d.Send(context.Background(), "x", nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestDemux_ConcurrentSendsGetTheirOwnResponses(t *testing.T) {
	t.Parallel()
	d, s := makeDemux(t)
	defer func() {
		_ = d.Close()
		s.close()
	}()

	const n = 16
	// Echo server: mirror each request's id back as result.
	go func() {
		for raw := range s.received {
			var r Request
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Errorf("unmarshal: %v", err)
				return
			}
			s.sendToClient(t, `{"id":`+itoa(int(r.ID))+`,"result":{"echo":`+itoa(int(r.ID))+`}}`)
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, err := d.Send(ctx, "ping", nil)
			if err != nil {
				errs <- err
				return
			}
			// Each goroutine must receive a response matching its own ID.
			var echo struct {
				Echo uint64 `json:"echo"`
			}
			if err := json.Unmarshal(resp.Result, &echo); err != nil {
				errs <- err
				return
			}
			if echo.Echo != resp.ID {
				errs <- errors.New("id/echo mismatch")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func expectNotification(t *testing.T, ctx context.Context, d *Demux) Notification {
	t.Helper()
	select {
	case n := <-d.Notifications():
		return n
	case <-ctx.Done():
		t.Fatal("expected notification within timeout")
		return Notification{}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
