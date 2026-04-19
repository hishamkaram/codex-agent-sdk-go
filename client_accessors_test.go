package codex

import (
	"context"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_ProcessID_ZeroBeforeConnect(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.ProcessID(); got != 0 {
		t.Fatalf("ProcessID before Connect = %d, want 0", got)
	}
}

func TestClient_SessionID_EmptyBeforeAnyThread(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.SessionID(); got != "" {
		t.Fatalf("SessionID with no threads = %q, want empty", got)
	}
}

func TestClient_SessionID_TracksLatestRegisteredThread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ops     []threadOp
		wantSID string
	}{
		{
			name: "single register",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
			},
			wantSID: "t1",
		},
		{
			name: "two registers — second wins",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opRegister, id: "t2"},
			},
			wantSID: "t2",
		},
		{
			name: "register then unregister matching id clears",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opUnregister, id: "t1"},
			},
			wantSID: "",
		},
		{
			name: "unregister of non-latest id keeps latest",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opRegister, id: "t2"},
				{kind: opUnregister, id: "t1"},
			},
			wantSID: "t2",
		},
		{
			name: "register / unregister / register — final wins",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opUnregister, id: "t1"},
				{kind: opRegister, id: "t2"},
			},
			wantSID: "t2",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(context.Background(), types.NewCodexOptions())
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			for _, op := range tt.ops {
				switch op.kind {
				case opRegister:
					c.registerThread(&Thread{client: c, id: op.id})
				case opUnregister:
					c.unregisterThread(op.id)
				}
			}
			if got := c.SessionID(); got != tt.wantSID {
				t.Fatalf("SessionID = %q, want %q", got, tt.wantSID)
			}
		})
	}
}

func TestClient_SessionID_ClearedByClose(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.registerThread(&Thread{client: c, id: "t1"})
	if got := c.SessionID(); got != "t1" {
		t.Fatalf("SessionID after register = %q, want %q", got, "t1")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := c.SessionID(); got != "" {
		t.Fatalf("SessionID after Close = %q, want empty", got)
	}
}

type threadOpKind int

const (
	opRegister threadOpKind = iota
	opUnregister
)

type threadOp struct {
	kind threadOpKind
	id   string
}
