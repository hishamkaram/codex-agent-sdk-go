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

// Reduced from the generated Codex 0.171.0 experimental TurnStartParams schema.
const turnProgramSchema = `{
	"type": "object",
	"required": ["input", "threadId"],
	"properties": {
		"cyberAccessProgram": {
			"anyOf": [{"$ref":"#/definitions/CyberAccessProgram"}, {"type":"null"}]
		}
	},
	"definitions": {
		"CyberAccessProgram": {
			"type": "string",
			"enum": ["standard", "daybreakBlue", "daybreakRed"]
		}
	}
}`

func TestTurnProgramSchemaRequiresExactOptionalEnum(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, schema string
		want         bool
	}{
		{"pinned contract", turnProgramSchema, true},
		{"field absent", strings.Replace(turnProgramSchema, "cyberAccessProgram", "unrelated", 1), false},
		{"required instead of optional", strings.Replace(turnProgramSchema, `"input", "threadId"`, `"input", "threadId", "cyberAccessProgram"`, 1), false},
		{"enum detached from request", strings.Replace(turnProgramSchema, "#/definitions/CyberAccessProgram", "#/definitions/Other", 1), false},
		{"unsupported enum", strings.Replace(turnProgramSchema, `, "daybreakRed"`, "", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			v2 := filepath.Join(root, "v2")
			if err := os.Mkdir(v2, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(v2, "TurnStartParams.json"), []byte(tc.schema), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := runtimeprobe.SupportsTurnProgram(root); err != nil || got != tc.want {
				t.Fatalf("schema match = %t, err = %v, want %t", got, err, tc.want)
			}
		})
	}
}

func TestThreadTurnProgramRequiresHandshakeAndProviderSchema(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	for _, tc := range []struct {
		name         string
		experimental bool
		schema       string
	}{
		{"handshake opt-in required", false, turnProgramSchema},
		{"field must be proved", true, `{"type":"object","properties":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := writeTurnProgramCLI(t, tc.schema)
			opts := types.NewCodexOptions().WithCLIPath(fixture.executable).WithEnvReplacement("GORACE=atexit_sleep_ms=0").WithExperimentalAPI(tc.experimental)
			_, thread := connectTurnProgramFixture(t, opts)
			// Mutating the original options after Connect cannot change what
			// the peer actually received in initialize.
			opts.WithExperimentalAPI(true)
			program := types.CyberAccessProgramStandard
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
			if !tc.experimental {
				if !types.IsFeatureNotEnabledError(err) {
					t.Fatalf("missing opt-in error = %v", err)
				}
				if count := readProgramProbeCount(t, fixture.probes); count != 0 {
					t.Fatalf("missing opt-in started %d schema probes", count)
				}
			} else if !errors.Is(err, ErrRuntimeControlsUnsupported) {
				t.Fatalf("missing runtime support error = %v", err)
			}
			if sent := readTurnStartPrograms(t, fixture.requests); len(sent) != 0 {
				t.Fatalf("unsupported option sent turn/start: %v", sent)
			}
		})
	}
}

func TestThreadTurnProgramUsesProofOnceAndPreservesProviderRejection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().WithCLIPath(fixture.executable).WithEnvReplacement("GORACE=atexit_sleep_ms=0").WithExperimentalAPI(true)
	_, thread := connectTurnProgramFixture(t, opts)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if probes := readProgramProbeCount(t, fixture.probes); probes != 0 {
		t.Fatalf("ordinary connection generated %d schemas", probes)
	}
	for _, program := range []types.CyberAccessProgram{
		types.CyberAccessProgramStandard, types.CyberAccessProgramDaybreakBlue, types.CyberAccessProgramDaybreakRed,
	} {
		turn, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
		if program == types.CyberAccessProgramDaybreakRed {
			var providerErr *types.RPCError
			if !errors.As(err, &providerErr) || providerErr.Code != -32123 || providerErr.Message != "test provider denied requested program" {
				t.Fatalf("account rejection was not propagated: %v", err)
			}
		} else if err != nil || turn.Status != "completed" {
			t.Fatalf("turn failed: result=%+v err=%v", turn, err)
		}
	}
	if _, err := thread.Run(ctx, "omitted option", nil); err != nil {
		t.Fatal(err)
	}
	got := readTurnStartPrograms(t, fixture.requests)
	assertStringsEqual(t, "wire program values", got, []string{`"standard"`, `"daybreakBlue"`, `"daybreakRed"`, ""})
	if probes := readProgramProbeCount(t, fixture.probes); probes != 1 {
		t.Fatalf("schema generated %d times, want one connection-local proof", probes)
	}
	_, secondConnection := connectTurnProgramFixture(t, opts)
	program := types.CyberAccessProgramStandard
	if _, err := secondConnection.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program}); err != nil {
		t.Fatal(err)
	}
	if probes := readProgramProbeCount(t, fixture.probes); probes != 2 {
		t.Fatalf("two connections generated %d schemas, want one each", probes)
	}
}

func TestTurnProgramRejectsApprovalCallbackReentry(t *testing.T) {
	t.Parallel()
	client := &Client{}
	ctx := context.WithValue(t.Context(), approvalClientKey{}, client)
	if err := client.requireTurnProgram(ctx, types.CyberAccessProgramStandard); !errors.Is(err, ErrApprovalCallbackReentry) {
		t.Fatalf("turn program reentry = %v, want ErrApprovalCallbackReentry", err)
	}
}

func TestTurnProgramComposesWithExistingOptions(t *testing.T) {
	t.Parallel()
	program := types.CyberAccessProgramStandard
	opts := &types.RunOptions{
		CyberAccessProgram: &program,
		OutputSchema:       &types.OutputSchema{Schema: map[string]any{"type": "object"}},
		CollaborationMode:  &types.CollaborationMode{Mode: types.CollaborationModePlan},
		ReasoningEffort:    "low",
		Skills:             []types.SkillInput{{Name: "example", Path: "/fixture/SKILL.md"}},
		Images:             []string{"/fixture/example.png"},
	}
	input, err := buildTurnInput("example", opts)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(buildTurnStartParams("thread", input, opts))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Program string `json:"cyberAccessProgram"`
		Effort  string `json:"effort"`
		Input   []struct {
			Type string `json:"type"`
		} `json:"input"`
		OutputSchema struct {
			Schema struct {
				Type string `json:"type"`
			} `json:"schema"`
		} `json:"outputSchema"`
		CollaborationMode struct {
			Mode string `json:"mode"`
		} `json:"collaborationMode"`
	}
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Program != "standard" || got.Effort != "low" || got.OutputSchema.Schema.Type != "object" || got.CollaborationMode.Mode != "plan" || len(got.Input) != 3 ||
		got.Input[0].Type != "text" || got.Input[1].Type != "skill" || got.Input[2].Type != "localImage" {
		t.Fatalf("composed turn/start params = %s", wire)
	}
}

type turnProgramFixture struct {
	executable string
	probes     string
	requests   string
	launchEnv  string
}

func writeTurnProgramCLI(t *testing.T, schema string) turnProgramFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := turnProgramFixture{
		executable: filepath.Join(dir, turnProgramPeerName),
		probes:     filepath.Join(dir, "schema-calls"),
		requests:   filepath.Join(dir, "calls.jsonl"),
		launchEnv:  filepath.Join(dir, "environment"),
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if linkErr := os.Symlink(executable, fixture.executable); linkErr != nil {
		t.Fatal(linkErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(schema), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	return fixture
}

func connectTurnProgramFixture(t *testing.T, opts *types.CodexOptions) (*Client, *Thread) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if closeErr := client.Close(closeCtx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if connectErr := client.Connect(ctx); connectErr != nil {
		t.Fatal(connectErr)
	}
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client, thread
}

func readTurnStartPrograms(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var programs []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var frame struct {
			Method string `json:"method"`
			Params struct {
				Program json.RawMessage `json:"cyberAccessProgram"`
			} `json:"params"`
		}
		if decodeErr := json.Unmarshal([]byte(line), &frame); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if frame.Method == "turn/start" {
			programs = append(programs, string(frame.Params.Program))
		}
	}
	return programs
}

func readProgramProbeCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "experimental\n")
}

func TestThreadTurnProgramRejectsRetargetedWrapper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	original := writeTurnProgramCLI(t, `{"type":"object","properties":{}}`)
	other := writeTurnProgramCLI(t, turnProgramSchema)
	dir := t.TempDir()
	target, wrapper := filepath.Join(dir, "target"), filepath.Join(dir, "launcher")
	if err := os.Symlink(original.executable, target); err != nil {
		t.Fatal(err)
	}
	writeClientExecutable(t, wrapper, fmt.Sprintf("#!/bin/sh\nexec \"$(readlink %q)\" \"$@\"\n", target))
	opts := types.NewCodexOptions().
		WithCLIPath(wrapper).
		WithEnv("GORACE=atexit_sleep_ms=0").
		WithExperimentalAPI(true)
	_, thread := connectTurnProgramFixture(t, opts)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other.executable, target); err != nil {
		t.Fatal(err)
	}
	program := types.CyberAccessProgramStandard
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
	if !errors.Is(err, ErrRuntimeControlsUnsupported) {
		t.Fatalf("wrapper passed running executable proof: %v", err)
	}
	if started := readTurnStartPrograms(t, original.requests); len(started) != 0 {
		t.Fatalf("wrapper allowed a turn before proof: %v", started)
	}
	if calls := readProgramProbeCount(t, other.probes); calls != 0 {
		t.Fatalf("replacement target supplied %d schema proofs", calls)
	}
}

func TestTurnProgramProbeReportsCancellationOwner(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running executable identity proof requires Linux /proc")
	}
	t.Parallel()
	for _, tc := range []struct {
		name string
		want error
	}{
		{"client close", types.ErrClientClosed},
		{"caller cancellation", context.Canceled},
		{"SDK deadline", ErrRuntimeControlsUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := writeTurnProgramCLI(t, turnProgramSchema)
			if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.executable), "schema-block"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			opts := types.NewCodexOptions().
				WithCLIPath(fixture.executable).
				WithEnvReplacement("GORACE=atexit_sleep_ms=0").
				WithExperimentalAPI(true)
			client, thread := connectTurnProgramFixture(t, opts)
			testCtx, cancelTest := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancelTest()
			callerCtx, cancelCaller := context.WithCancel(testCtx)
			defer cancelCaller()
			result := make(chan error, 1)
			go func() {
				program := types.CyberAccessProgramStandard
				_, err := thread.Run(callerCtx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program})
				result <- err
			}()
			waitForProgramProbe(t, testCtx, fixture.probes)
			switch {
			case errors.Is(tc.want, types.ErrClientClosed):
				if err := client.Close(testCtx); err != nil {
					t.Fatal(err)
				}
			case errors.Is(tc.want, context.Canceled):
				cancelCaller()
			}
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("probe error = %v, want %v", err, tc.want)
				}
				if !errors.Is(tc.want, context.Canceled) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
					t.Fatalf("SDK cancellation exposed as caller cancellation: %v", err)
				}
			case <-testCtx.Done():
				t.Fatal("probe did not return")
			}
			if started := readTurnStartPrograms(t, fixture.requests); len(started) != 0 {
				t.Fatalf("canceled proof allowed turn/start: %v", started)
			}
		})
	}
}

func waitForProgramProbe(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for readProgramProbeCount(t, path) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("probe did not reach schema generation")
		case <-ticker.C:
		}
	}
}
