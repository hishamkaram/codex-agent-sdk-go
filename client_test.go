package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestNewClient_NilOptsIsError(t *testing.T) {
	t.Parallel()
	_, err := NewClient(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil opts")
	}
	if !strings.Contains(err.Error(), "opts must not be nil") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewClient_ReturnsUnconnected(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatal(err)
	}
	if c.connected.Load() {
		t.Fatal("new client must not be connected")
	}
	if c.closed.Load() {
		t.Fatal("new client must not be closed")
	}
}

func TestClient_CloseIsIdempotentPreConnect(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestClient_StartThreadBeforeConnectIsError(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	_, err := c.StartThread(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when StartThread before Connect")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_ResumeThreadEmptyIDIsError(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	// Even though we haven't connected yet, the empty-ID check should
	// still fire (the not-connected check fires first here actually, but
	// both are errors).
	_, err := c.ResumeThread(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty threadID")
	}
}
