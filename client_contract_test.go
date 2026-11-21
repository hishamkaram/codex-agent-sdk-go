package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClientInitializeResultAndThreadModel(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("running executable proof requires Linux /proc")
	}

	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().WithCLIPath(fixture.executable).WithEnvReplacement("GORACE=atexit_sleep_ms=0")
	client, thread := connectTurnProgramFixture(t, opts)
	want := InitializeResult{UserAgent: "sdk-peer", CodexHome: "/fixture", PlatformFamily: "unix", PlatformOs: "linux"}
	if got := client.InitializeResult(); got != want {
		t.Errorf("InitializeResult() = %+v, want %+v", got, want)
	}
	if got := thread.Model(); got != "fixture-model" {
		t.Errorf("Model() = %q, want fixture-model", got)
	}
}

type queryExit struct {
	code      int
	requested bool
	cause     error
}

type queryObserver struct {
	types.NopObserver
	exits chan queryExit
}

func (o queryObserver) OnSubprocessExit(code int, requested bool, cause error) {
	select {
	case o.exits <- queryExit{code: code, requested: requested, cause: cause}:
	default:
	}
}

func TestQueryStreamsOneTurnAndClosesProcess(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("running executable proof requires Linux /proc")
	}

	for _, method := range []string{"", "thread/start", "turn/start"} {
		name := method
		if name == "" {
			name = "completed turn"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := writeTurnProgramCLI(t, turnProgramSchema)
			if method != "" {
				rejectFile := filepath.Join(filepath.Dir(fixture.executable), "reject-method")
				if err := os.WriteFile(rejectFile, []byte(method), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			exits := make(chan queryExit, 1)
			opts := types.NewCodexOptions().WithCLIPath(fixture.executable).
				WithEnvReplacement("GORACE=atexit_sleep_ms=0").WithObserver(queryObserver{exits: exits})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			events, err := Query(ctx, "one-off prompt", opts)
			if method == "" {
				if err != nil {
					t.Fatal(err)
				}
				started, ok := awaitExecutionTest(t, ctx, events).(*types.TurnStarted)
				if !ok || started.TurnID == "" {
					t.Fatalf("first event = %#v, want a started turn", started)
				}
				completed, ok := awaitExecutionTest(t, ctx, events).(*types.TurnCompleted)
				if !ok || completed.TurnID != started.TurnID || completed.Status != "completed" {
					t.Fatalf("second event = %#v, want completion of %q", completed, started.TurnID)
				}
				select {
				case next, open := <-events:
					if open {
						t.Fatalf("additional event: %#v", next)
					}
				case <-ctx.Done():
					t.Fatal("Query did not close its event stream")
				}
			} else {
				var provider *types.RPCError
				if events != nil || !errors.As(err, &provider) || provider.Code != -32123 ||
					provider.Message != "test provider rejected request" {
					t.Fatalf("Query returned events=%v, err=%v for rejection of %s", events, err, method)
				}
			}
			if exit := awaitExecutionTest(t, ctx, exits); !exit.requested || exit.code != 0 || exit.cause != nil {
				t.Fatalf("process exit = %+v, want clean SDK shutdown", exit)
			}

			calls := approvedPlanPeerCalls(t, fixture, "turn/start")
			if method == "thread/start" {
				if len(calls) != 0 {
					t.Fatalf("Query sent %d turns after thread start rejection", len(calls))
				}
				return
			}
			if len(calls) != 1 {
				t.Fatalf("Query sent %d turns, want one", len(calls))
			}
			var params struct {
				ThreadID string `json:"threadId"`
				Input    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"input"`
			}
			if err := json.Unmarshal(calls[0], &params); err != nil {
				t.Fatal(err)
			}
			if params.ThreadID != "t1" || len(params.Input) != 1 || params.Input[0].Type != "text" ||
				params.Input[0].Text != "one-off prompt" {
				t.Fatalf("turn/start sent %s", calls[0])
			}
		})
	}
}

func TestStartupAppliesOptionsWithoutSpawning(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unavailable-codex")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var configured string
	if err := Startup(ctx, WithStartupCLIPath(path), func(cfg *startupConfig) {
		configured = cfg.cliPath
	}); err != nil {
		t.Fatalf("Startup tried to initialize an unavailable CLI: %v", err)
	}
	if configured != path {
		t.Errorf("Startup observed CLI path %q, want %q", configured, path)
	}
}
