package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type routedEvent struct {
	threadID, method, activeTurnID string
}

type dispatchSink struct {
	record *threadexec.Record
	events []routedEvent
	gaps   []Gap
	closed bool
}

func (s *dispatchSink) Publish(id string, event types.ThreadEvent) {
	s.events = append(s.events, routedEvent{id, event.EventMethod(), s.record.ActiveID()})
}

func (s *dispatchSink) Fail(gap Gap) { s.gaps = append(s.gaps, gap) }

func (s *dispatchSink) Close() { s.closed = true }

func TestDispatchAppliesLifecycleBeforePublishingAndRetiresRecordsOnEOF(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dispatched, stop := context.WithCancel(ctx)
	defer stop()
	logger := sdklog.NewLoggerFromZap(nil)
	wire := strings.NewReader(`{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"turn"}}}
{"method":"future/event","params":{"threadId":"child","turnId":"child-turn"}}
{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn","status":"completed"}}}
{"method":"turn/started","params":{"turn":{"id":"missing-thread"}}}
`)
	demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(wire), jsonrpc.NewLineWriter(io.Discard),
		logger, jsonrpc.WithOrderedServerMessages())
	record := threadexec.NewRecord(true)
	sink := &dispatchSink{record: record}
	session := New(types.NewCodexOptions(), logger, sink)
	if err := session.StartDispatcher(dispatched, stop, demux, session.Dispatch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := session.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	session.Threads().Register("thread", record, session.Connected)
	demux.Run(ctx)

	select {
	case <-session.DispatcherDone():
	case <-ctx.Done():
		t.Fatal("dispatcher did not exit after EOF")
	}
	want := []routedEvent{
		{threadID: "thread", method: "turn/started", activeTurnID: "turn"},
		{threadID: "child", method: "future/event", activeTurnID: "turn"},
		{threadID: "thread", method: "turn/completed"},
	}
	if !slices.Equal(sink.events, want) {
		t.Errorf("published events = %+v, want %+v", sink.events, want)
	}
	if len(sink.gaps) != 2 || sink.gaps[0].Kind != ParseGap || sink.gaps[0].Method != "turn/started" ||
		!errors.Is(sink.gaps[0].Cause, errThreadEventIdentityMissing) ||
		sink.gaps[1].Kind != SourceGap || sink.gaps[1].Cause != nil {
		t.Errorf("gaps = %+v, want missing identity followed by clean EOF", sink.gaps)
	}
	if !sink.closed || !record.Closed() || session.Connected() || session.Closed() {
		t.Errorf("after EOF sink closed=%t record closed=%t session ready=%t closed=%t",
			sink.closed, record.Closed(), session.Connected(), session.Closed())
	}
}

func TestPendingActivityMatchesOwnerAndFailsClosedOnUndecodableEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		method string
		params string
		want   bool
	}{
		{"irrelevant method", "item/started", `{"threadId":"thread"}`, false},
		{"matching root", "turn/started", `{"threadId":"thread","turn":{"id":"root"}}`, true},
		{"nested turn", "turn/started", `{"threadId":"thread","turn":{"id":"child","rootTurnId":"root"}}`, false},
		{"foreign root", "turn/started", `{"threadId":"other","turn":{"id":"root"}}`, false},
		{"missing identity", "turn/started", `{"turn":{"id":"root"}}`, true},
		{"failed parse matching", "turn/started", `{"threadId":"thread","turn":17}`, true},
		{"failed parse foreign", "turn/started", `{"threadId":"other","turn":17}`, false},
		{"active", "thread/status/changed", `{"threadId":"thread","status":{"type":"active"}}`, true},
		{"idle", "thread/status/changed", `{"threadId":"thread","status":{"type":"idle"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			notice := jsonrpc.Notification{Method: test.method, Params: json.RawMessage(test.params)}
			if got := pendingActivityAffectsThread(notice, "thread"); got != test.want {
				t.Errorf("pending activity affects thread = %t, want %t", got, test.want)
			}
		})
	}
	if !pendingActivityAffectsThread(jsonrpc.Notification{DecodeError: io.ErrUnexpectedEOF}, "thread") {
		t.Error("an undecodable frame may hide activity for any thread")
	}
}
