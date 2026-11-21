package session

import (
	"fmt"
	"strings"
	"testing"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRedactedStderrDiagnosticTailUsesAllowlistedDiagnostics(t *testing.T) {
	t.Parallel()

	secret := "sk-review-secret"
	got := redactedStderrDiagnosticTail(strings.Join([]string{
		"control protocol initialization failed OPENAI_API_KEY=" + secret,
		"DATABASE_URL=postgres://user:" + secret + "@localhost/db",
		"Authorization: Bearer " + strings.Repeat("x", stderrDiagnosticTailLimit) + secret,
	}, "\n"))
	if strings.Contains(got, secret) {
		t.Fatalf("redacted stderr tail leaked secret: %q", got)
	}
	if strings.Contains(got, "DATABASE_URL") || strings.Contains(got, "Authorization") {
		t.Fatalf("redacted stderr tail leaked raw stderr fields: %q", got)
	}
	if !strings.Contains(got, "control protocol initialization failed") {
		t.Fatalf("redacted stderr tail = %q, want allowlisted diagnostic", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("redacted stderr tail = %q, want redaction marker", got)
	}
}

func TestClient_LogInitializeFailureOmitsRPCMessage(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	c := Session{logger: sdklog.NewLoggerFromZap(zap.New(core))}
	secret := "DATABASE_URL=postgres://secret"
	c.logInitializeFailure(types.NewRPCError(-32000, secret, []byte(`{"cookie":"secret"}`)), "")

	entries := logs.FilterMessage("codex.Client.Connect: initialize failed").All()
	if len(entries) == 0 {
		t.Fatal("missing initialize failure diagnostic log")
	}
	fields := entries[0].ContextMap()
	if _, ok := fields["error"]; ok {
		t.Fatalf("initialize failure log included raw error field: %#v", fields)
	}
	if strings.Contains(fmt.Sprint(fields), secret) || strings.Contains(fmt.Sprint(fields), "cookie") {
		t.Fatalf("initialize failure log leaked RPC payload: %#v", fields)
	}
	if fields["error_kind"] != "rpc_error" {
		t.Fatalf("error_kind = %#v, want rpc_error; fields=%#v", fields["error_kind"], fields)
	}
	if fmt.Sprint(fields["rpc_error_code"]) != "-32000" {
		t.Fatalf("rpc_error_code = %#v, want -32000; fields=%#v", fields["rpc_error_code"], fields)
	}
}

func TestBuildInitializeParams_IncludesNotificationOptOut(t *testing.T) {
	t.Parallel()
	opts := types.NewCodexOptions().
		WithExperimentalAPI(true).
		WithOptOutNotificationMethods("process/outputDelta", "warning")

	params := buildInitializeParams(opts)
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %T", params["capabilities"])
	}
	if capabilities["experimentalApi"] != true {
		t.Fatalf("experimentalApi = %#v", capabilities["experimentalApi"])
	}
	got, ok := capabilities["optOutNotificationMethods"].([]string)
	if !ok {
		t.Fatalf("optOutNotificationMethods = %T", capabilities["optOutNotificationMethods"])
	}
	if len(got) != 2 || got[0] != "process/outputDelta" || got[1] != "warning" {
		t.Fatalf("optOutNotificationMethods = %#v", got)
	}
}
