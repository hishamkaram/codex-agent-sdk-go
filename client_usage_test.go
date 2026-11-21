package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_BufferedUsageRequiresMeasuredTurn(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	dispatched := make(chan struct{}, 1)
	count := -1
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method == "thread/start" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		}
		count++
		dispatched <- struct{}{}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(fmt.Sprintf(`{"turn":{"id":"U%d"}}`, count))}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		id, frames string
		available  bool
		total      int64
		usage      types.TokenUsage
		zero       bool
	}{
		{"U0", "", true, 0, types.TokenUsage{}, true},
		{"U1", `{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U1","tokenUsage":{"last":{"inputTokens":5,"cachedInputTokens":1,"cacheWriteInputTokens":2,"outputTokens":2,"totalTokens":7},"total":{"inputTokens":5,"cachedInputTokens":1,"cacheWriteInputTokens":2,"outputTokens":2,"totalTokens":7},"modelContextWindow":1000}}}`, true, 7, types.TokenUsage{InputTokens: 5, CachedInputTokens: 1, CacheWriteInputTokens: 2, OutputTokens: 2, TotalTokens: 7}, false},
		{"U2", `{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U2","tokenUsage":{"last":{"inputTokens":5,"cachedInputTokens":1,"cacheWriteInputTokens":2,"outputTokens":2,"totalTokens":7},"total":{"inputTokens":5,"cachedInputTokens":1,"cacheWriteInputTokens":2,"outputTokens":2,"totalTokens":7},"modelContextWindow":1000}}}
{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U2","tokenUsage":{"last":{"inputTokens":3,"cacheWriteInputTokens":2,"outputTokens":1,"totalTokens":4},"total":{"inputTokens":8,"cachedInputTokens":1,"cacheWriteInputTokens":4,"outputTokens":3,"totalTokens":11},"modelContextWindow":1000}}}
{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U2","tokenUsage":{"last":{"inputTokens":5,"cachedInputTokens":2,"outputTokens":2,"totalTokens":7},"total":{"inputTokens":13,"cachedInputTokens":3,"cacheWriteInputTokens":4,"outputTokens":5,"totalTokens":18},"modelContextWindow":1000}}}`, true, 18, types.TokenUsage{InputTokens: 8, CachedInputTokens: 2, CacheWriteInputTokens: 2, OutputTokens: 3, TotalTokens: 11}, false},
		{"U3", "", false, 0, types.TokenUsage{}, false},
		// The first increment actually belongs to silent U3, despite U4's ID.
		{"U4", `{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U4","tokenUsage":{"last":{"inputTokens":1,"cacheWriteInputTokens":1,"outputTokens":1,"totalTokens":2},"total":{"inputTokens":14,"cachedInputTokens":3,"cacheWriteInputTokens":5,"outputTokens":6,"totalTokens":20},"modelContextWindow":1000}}}
{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U4","tokenUsage":{"last":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"total":{"inputTokens":15,"cachedInputTokens":3,"cacheWriteInputTokens":5,"outputTokens":7,"totalTokens":22},"modelContextWindow":1000}}}`, false, 22, types.TokenUsage{}, false},
		{"U5", `{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U5","tokenUsage":{"last":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"total":{"inputTokens":16,"cachedInputTokens":3,"cacheWriteInputTokens":5,"outputTokens":8,"totalTokens":24},"modelContextWindow":1000}}}
{"method":"thread/compacted","params":{"threadId":"T","turnId":"U5"}}
{"method":"thread/tokenUsage/updated","params":{"threadId":"T","turnId":"U5","tokenUsage":{"last":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"total":{"inputTokens":17,"cachedInputTokens":3,"cacheWriteInputTokens":5,"outputTokens":9,"totalTokens":26},"modelContextWindow":1000}}}`, false, 26, types.TokenUsage{}, false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			type outcome struct {
				turn *Turn
				err  error
			}
			result := make(chan outcome, 1)
			go func() {
				value, runErr := thread.Run(ctx, "measure usage", nil)
				result <- outcome{turn: value, err: runErr}
			}()
			awaitExecutionTest(t, ctx, dispatched)
			peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": tc.id}))
			for frame := range strings.SplitSeq(tc.frames, "\n") {
				if frame != "" {
					peer.push(json.RawMessage(frame))
				}
			}
			completion := map[string]any{"threadId": "T", "turnId": tc.id, "status": "completed"}
			if tc.zero {
				completion["usage"] = map[string]int{}
			}
			peer.push(notify("turn/completed", completion))
			got := awaitExecutionTest(t, ctx, result)
			if got.err != nil || got.turn.UsageAvailable != tc.available || got.turn.Usage != tc.usage {
				t.Fatalf("usage result = %+v, err = %v", got.turn, got.err)
			}
			var lastStreamingUsage types.TokenUsage
			for _, event := range got.turn.Events {
				if usage, ok := event.(*types.TokenUsageUpdated); ok {
					lastStreamingUsage = usage.Usage
				}
			}
			if lastStreamingUsage.TotalTokens != tc.total {
				t.Fatalf("legacy streaming usage = %+v, want total %d", lastStreamingUsage, tc.total)
			}
		})
	}
}
