package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestPrepareApprovedPlanHandoffUsesFreshDestinationAndExactPrompt(t *testing.T) {
	t.Parallel()
	fixture := writeApprovedPlanCLI(t, approvedPlanPeerSchemas())
	client, source := connectTurnProgramFixture(t, approvedPlanCLIOptions(fixture, true))
	ctx := approvedPlanContext(t)
	destination := startApprovedPlanThread(t, ctx, client)
	const markdown = "  # Approved plan\r\n\r\n- café ✓  \n"
	const attachment = "/app-server-only/folder/approved.md"

	prepared, err := destination.PrepareApprovedPlan(ctx, markdown, &types.PrepareApprovedPlanOptions{AttachmentPath: attachment})
	if err != nil || prepared.Prompt != approvedPlanPeerPrompt {
		t.Fatalf("prepare = %+v, %v", prepared, err)
	}
	if _, err := destination.Run(ctx, prepared.Prompt, nil); err != nil {
		t.Fatal(err)
	}
	next := startApprovedPlanThread(t, ctx, client)
	if _, err := next.PrepareApprovedPlan(ctx, "other approved plan", nil); err != nil {
		t.Fatal(err)
	}
	if probes := readProgramProbeCount(t, fixture.probes); probes != 1 {
		t.Fatalf("reused preparation generated %d schemas, want one", probes)
	}
	if err := client.requireTurnProgram(ctx, types.CyberAccessProgramStandard); err != nil {
		t.Fatal(err)
	}
	if probes := readProgramProbeCount(t, fixture.probes); probes != 2 {
		t.Fatalf("separate capability generated %d schemas, want two", probes)
	}

	preparations := approvedPlanPeerCalls(t, fixture, "thread/prepareApprovedPlan")
	if len(preparations) != 2 {
		t.Fatalf("preparations = %s, want two", preparations)
	}
	var actual map[string]string
	if err := json.Unmarshal(preparations[0], &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual) != 3 || actual["threadId"] == source.ID() || actual["threadId"] != destination.ID() ||
		actual["markdown"] != markdown || actual["attachmentPath"] != attachment {
		t.Fatalf("first preparation = %s", preparations[0])
	}
	if err := json.Unmarshal(preparations[1], &actual); err != nil {
		t.Fatal(err)
	}
	var omitted map[string]json.RawMessage
	if err := json.Unmarshal(preparations[1], &omitted); err != nil {
		t.Fatal(err)
	}
	if len(omitted) != 2 || actual["threadId"] != next.ID() || actual["markdown"] != "other approved plan" {
		t.Fatalf("second preparation = %s", preparations[1])
	}
	assertApprovedPlanPeerTurn(t, fixture, destination.ID(), approvedPlanPeerPrompt)
}

func TestPrepareApprovedPlanRejectsUnprovedContractAndMissingOptIn(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		experimental bool
		schemas      map[string]string
	}{
		{"missing opt-in", false, approvedPlanPeerSchemas()},
		{"unproved schema", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := writeApprovedPlanCLI(t, test.schemas)
			_, thread := connectTurnProgramFixture(t, approvedPlanCLIOptions(fixture, test.experimental))
			_, err := thread.PrepareApprovedPlan(approvedPlanContext(t), "approved plan", nil)
			if test.experimental && !errors.Is(err, ErrRuntimeControlsUnsupported) ||
				!test.experimental && !types.IsFeatureNotEnabledError(err) {
				t.Fatalf("gate error = %v", err)
			}
			if got := approvedPlanPeerCalls(t, fixture, "thread/prepareApprovedPlan"); len(got) != 0 {
				t.Fatalf("gate sent preparation: %s", got)
			}
		})
	}
}

const approvedPlanErrorPrefix = "codex.Thread.PrepareApprovedPlan: "

func TestPrepareApprovedPlanCancellationFencesOnlyDestination(t *testing.T) {
	t.Parallel()
	fixture := writeApprovedPlanCLI(t, approvedPlanPeerSchemas())
	client, source := connectTurnProgramFixture(t, approvedPlanCLIOptions(fixture, true))
	ctx := approvedPlanContext(t)
	destination := startApprovedPlanThread(t, ctx, client)
	duplicate := newThread(client, destination.ID())
	client.registerThread(duplicate)
	events, err := client.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}

	prepareCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	prepared := make(chan error, 1)
	go func() {
		_, err := destination.PrepareApprovedPlan(prepareCtx, approvedPlanPeerHold, nil)
		prepared <- err
	}()
	for {
		envelope := awaitExecutionTest(t, ctx, events)
		if event, ok := envelope.Event.(*types.UnknownEvent); ok && event.Method == approvedPlanPeerReceived {
			break
		}
	}
	if _, err := duplicate.PrepareApprovedPlan(ctx, "must not send duplicate", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) ||
		!strings.HasPrefix(err.Error(), approvedPlanErrorPrefix) {
		t.Fatalf("competing preparation = %v", err)
	}
	if _, err := duplicate.Run(ctx, "must not race preparation", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("competing turn = %v", err)
	}

	cancel()
	if err := awaitExecutionTest(t, ctx, prepared); !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), approvedPlanErrorPrefix) {
		t.Fatalf("preparation cancellation = %v", err)
	}
	if _, err := duplicate.Run(ctx, "must not start on ambiguous destination", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("turn after ambiguous preparation = %v", err)
	}
	if _, err := source.Run(ctx, "original remains usable", nil); err != nil {
		t.Fatal(err)
	}
	if got := approvedPlanPeerCalls(t, fixture, "thread/prepareApprovedPlan"); len(got) != 1 {
		t.Fatalf("preparations = %s, want one", got)
	}
	assertApprovedPlanPeerTurn(t, fixture, source.ID(), "original remains usable")
}

func TestPrepareApprovedPlanPreservesRejectionAndFencesMalformedReply(t *testing.T) {
	t.Parallel()
	fixture := writeApprovedPlanCLI(t, approvedPlanPeerSchemas())
	client, source := connectTurnProgramFixture(t, approvedPlanCLIOptions(fixture, true))
	ctx := approvedPlanContext(t)
	rejected := startApprovedPlanThread(t, ctx, client)
	if _, err := rejected.PrepareApprovedPlan(ctx, approvedPlanPeerReject, nil); !approvedPlanPeerRPCError(err) ||
		!strings.HasPrefix(err.Error(), approvedPlanErrorPrefix) {
		t.Fatalf("provider rejection = %v", err)
	}
	if _, err := rejected.PrepareApprovedPlan(ctx, "explicit retry after definitive rejection", nil); err != nil {
		t.Fatalf("definitive rejection left destination reserved: %v", err)
	}

	malformed := startApprovedPlanThread(t, ctx, client)
	if _, err := malformed.PrepareApprovedPlan(ctx, approvedPlanPeerMalformed, nil); err == nil ||
		!strings.HasPrefix(err.Error(), approvedPlanErrorPrefix) {
		t.Fatalf("malformed reply = %v, want a preparation error with caller context", err)
	}
	if _, err := malformed.Run(ctx, "must not start without a usable prompt", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("turn after malformed reply = %v", err)
	}
	if _, err := source.Run(ctx, "original after failures", nil); err != nil {
		t.Fatal(err)
	}
	assertApprovedPlanPeerTurn(t, fixture, source.ID(), "original after failures")
}

func approvedPlanPeerRPCError(err error) bool {
	var rpcErr *types.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32177 && rpcErr.Message == "fixture rejected preparation"
}

func assertApprovedPlanPeerTurn(t *testing.T, fixture turnProgramFixture, threadID, expected string) {
	t.Helper()
	calls := approvedPlanPeerCalls(t, fixture, "turn/start")
	if len(calls) != 1 {
		t.Fatalf("turn/start calls = %s, want one", calls)
	}
	var turn struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
	}
	if err := json.Unmarshal(calls[0], &turn); err != nil {
		t.Fatal(err)
	}
	if turn.ThreadID != threadID || len(turn.Input) != 1 || turn.Input[0].Type != "text" || turn.Input[0].Text != expected {
		t.Fatalf("unexpected turn/start: %s", calls[0])
	}
}

func approvedPlanPeerCalls(t *testing.T, fixture turnProgramFixture, method string) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(fixture.requests)
	if err != nil {
		t.Fatal(err)
	}
	var calls []json.RawMessage
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		if request.Method == method {
			calls = append(calls, request.Params)
		}
	}
	return calls
}

func writeApprovedPlanCLI(t *testing.T, schemas map[string]string) turnProgramFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("running executable proof requires Linux /proc")
	}
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	writeApprovedPlanPeerSchemas(t, filepath.Join(filepath.Dir(fixture.executable), "approved-plan"), schemas)
	return fixture
}

func approvedPlanCLIOptions(fixture turnProgramFixture, experimental bool) *types.CodexOptions {
	return types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement("GORACE=atexit_sleep_ms=0").
		WithExperimentalAPI(experimental)
}

func startApprovedPlanThread(t *testing.T, ctx context.Context, client *Client) *Thread {
	t.Helper()
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return thread
}

func approvedPlanContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

const (
	approvedPlanPeerPrompt    = "\n Implement the provider-approved plan. \n"
	approvedPlanPeerHold      = "wait without a preparation response"
	approvedPlanPeerReject    = "reject this preparation"
	approvedPlanPeerMalformed = "acknowledge with an invalid prompt key"
	approvedPlanPeerReceived  = "fixture/approved-plan-received"
)

// Reduced independently from the Codex 0.171.0 experimental output.
const approvedPlanPeerParameters = `{"type":"object","required":["markdown","threadId"],"properties":{
	"threadId":{"type":"string"},
	"markdown":{"type":"string"},
	"attachmentPath":{"type":["string","null"]}
}}`

const approvedPlanPeerResponse = `{"type":"object","required":["prompt"],"properties":{"prompt":{"type":"string"}}}`

func approvedPlanPeerSchemas() map[string]string {
	return map[string]string{
		"ClientRequest.json": fmt.Sprintf(`{"oneOf":[{
			"type":"object","required":["id","method","params"],"properties":{
				"id":{"$ref":"#/definitions/RequestId"},
				"method":{"type":"string","enum":["thread/prepareApprovedPlan"]},
				"params":{"$ref":"#/definitions/ThreadPrepareApprovedPlanParams"}
			}
		}],"definitions":{"RequestId":{"type":"integer"},"ThreadPrepareApprovedPlanParams":%s}}`, approvedPlanPeerParameters),
		"v2/ThreadPrepareApprovedPlanParams.json":   approvedPlanPeerParameters,
		"v2/ThreadPrepareApprovedPlanResponse.json": approvedPlanPeerResponse,
	}
}

func copyApprovedPlanPeerSchema(fixtureDir, outDir string) error {
	for path := range approvedPlanPeerSchemas() {
		data, err := os.ReadFile(filepath.Join(fixtureDir, "approved-plan", path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, path), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func replyApprovedPlanPeer(frame turnProgramPeerRequest) error {
	var err error
	switch frame.Params.Markdown {
	case approvedPlanPeerHold:
		_, err = fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":{\"threadId\":%q}}\n", approvedPlanPeerReceived, frame.Params.ThreadID)
	case approvedPlanPeerReject:
		_, err = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-32177,\"message\":\"fixture rejected preparation\",\"data\":{\"reason\":\"fixture\"}}}\n", frame.ID)
	case approvedPlanPeerMalformed:
		_, err = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"Prompt\":\"wrong field name\"}}\n", frame.ID)
	default:
		_, err = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"prompt\":%q}}\n", frame.ID, approvedPlanPeerPrompt)
	}
	return err
}

func TestApprovedPlanSchemaRequiresMethodAndBothShapes(t *testing.T) {
	t.Parallel()
	const (
		binding = "ClientRequest.json"
		params  = "v2/ThreadPrepareApprovedPlanParams.json"
		reply   = "v2/ThreadPrepareApprovedPlanResponse.json"
	)
	for _, test := range []struct {
		name, path, old, replacement string
	}{
		{name: "pinned contract"},
		{"missing method", binding, "thread/prepareApprovedPlan", "thread/other"},
		{"wrong parameter binding", binding, "#/definitions/ThreadPrepareApprovedPlanParams", "#/definitions/OtherParams"},
		{"conflicting bound schema", binding, `"markdown":{"type":"string"}`, `"markdown":{"type":"integer"}`},
		{"attachment made required", params, `"required":["markdown","threadId"]`, `"required":["markdown","threadId","attachmentPath"]`},
		{"unexpected required request field", params, `"required":["markdown","threadId"]`, `"required":["markdown","threadId","revision"]`},
		{"attachment no longer nullable", params, `["string","null"]`, `"string"`},
		{"wrong prompt type", reply, `"type":"string"`, `"type":"integer"`},
		{"optional prompt", reply, `"required":["prompt"]`, `"required":[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			schemas := approvedPlanPeerSchemas()
			if test.path != "" {
				original := schemas[test.path]
				schemas[test.path] = strings.Replace(original, test.old, test.replacement, 1)
				if schemas[test.path] == original {
					t.Fatal("fixture mutation did not change its input")
				}
			}
			writeApprovedPlanPeerSchemas(t, root, schemas)
			if got, err := runtimeprobe.SupportsApprovedPlan(root); err != nil || got != (test.path == "") {
				t.Fatalf("schema support = %t, %v", got, err)
			}
		})
	}
}

func writeApprovedPlanPeerSchemas(t *testing.T, root string, schemas map[string]string) {
	t.Helper()
	for path, data := range schemas {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
