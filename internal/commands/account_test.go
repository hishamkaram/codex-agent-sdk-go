package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestUploadFeedbackWarnsBeforeOptInSend(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		includeLogs bool
		sendErr     error
	}{
		{name: "log opt-in with send failure", includeLogs: true, sendErr: errors.New("test send failure")},
		{name: "log opt-out with receipt"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			warnings := 0
			warn := func(classification string) {
				if classification != "test-category" {
					t.Fatalf("warned about category %q", classification)
				}
				warnings++
			}
			send := func(_ context.Context, callerName, method string, params any) (json.RawMessage, error) {
				if (warnings == 1) != tt.includeLogs {
					t.Fatalf("warnings before send = %d, include logs = %v", warnings, tt.includeLogs)
				}
				if callerName != "UploadFeedback" || method != "feedback/upload" {
					t.Fatalf("caller/method = %q/%q", callerName, method)
				}
				wire, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				var payload struct {
					Category    string `json:"classification"`
					IncludeLogs *bool  `json:"includeLogs"`
				}
				if err := json.Unmarshal(wire, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Category != "test-category" || payload.IncludeLogs == nil || *payload.IncludeLogs != tt.includeLogs {
					t.Fatalf("incorrect payload: %s", wire)
				}
				if tt.sendErr != nil {
					return nil, tt.sendErr
				}
				return json.RawMessage(`{"threadId":"thread-1"}`), nil
			}

			receipt, err := UploadFeedback(t.Context(), send, types.FeedbackReport{
				Classification: "test-category",
				IncludeLogs:    tt.includeLogs,
			}, warn)
			if !errors.Is(err, tt.sendErr) {
				t.Fatalf("err = %v, want %v", err, tt.sendErr)
			}
			if tt.sendErr != nil {
				if receipt != nil || warnings != 1 {
					t.Fatalf("receipt = %v, warnings = %d, want nil and one warning", receipt, warnings)
				}
			} else if receipt == nil || receipt.ThreadID != "thread-1" || warnings != 0 {
				t.Fatalf("receipt = %v, warnings = %d, want thread-1 and no warning", receipt, warnings)
			}
		})
	}
}

func TestUploadFeedbackWithMissingClassificationDoesNotWarnOrSend(t *testing.T) {
	t.Parallel()

	send := func(context.Context, string, string, any) (json.RawMessage, error) {
		t.Fatal("sent feedback without classification")
		return nil, nil
	}
	warn := func(string) { t.Fatal("warned about logs for an invalid report") }
	receipt, err := UploadFeedback(t.Context(), send, types.FeedbackReport{IncludeLogs: true}, warn)
	if receipt != nil || err == nil || !strings.Contains(err.Error(), "Classification is required") {
		t.Fatalf("receipt = %v, err = %v, want classification validation error", receipt, err)
	}
}
