//go:build integration

package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const (
	mcp0171StdioValue = "sdk-stdio-0171"
	mcp0171HTTPValue  = "sdk-http-0171"
	mcp0171FakeBearer = "sdk-test-token-0171"
)

func codex0171Options(t *testing.T, ctx context.Context, home, workspace string, env ...string) *types.CodexOptions {
	t.Helper()
	cli := os.Getenv("CODEX_SDK_0171_CLI")
	if cli == "" {
		t.Skip("set CODEX_SDK_0171_CLI to a native Codex 0.171.0 executable")
	}
	if !filepath.IsAbs(cli) {
		t.Fatal("CODEX_SDK_0171_CLI must be absolute")
	}
	selectedEnv := append([]string{"CODEX_HOME=" + home, "HOME=" + home, "PATH=" + os.Getenv("PATH")}, env...)
	version := exec.CommandContext(ctx, cli, "--version")
	version.Env = selectedEnv
	version.Dir = workspace
	observed, err := version.Output()
	if err != nil {
		t.Fatalf("check native Codex version: %v", err)
	}
	if strings.TrimSpace(string(observed)) != "codex-cli 0.171.0" {
		t.Fatalf("need Codex 0.171.0, received %q", strings.TrimSpace(string(observed)))
	}
	t.Log(strings.TrimSpace(string(observed)))
	return types.NewCodexOptions().WithCLIPath(cli).WithCwd(workspace).
		WithEnvReplacement(selectedEnv...).WithApprovalPolicy(types.ApprovalNever)
}

func connect0171(t *testing.T, ctx context.Context, opts *types.CodexOptions) *codex.Client {
	t.Helper()
	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Close(cleanup); err != nil {
			t.Errorf("close client: %v", err)
		}
	})
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect client: %v", err)
	}
	return client
}

func TestCodex0171SandboxConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	home, workspace := t.TempDir(), t.TempDir()
	opts := codex0171Options(t, ctx, home, workspace).WithSandbox("")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("sandbox_mode = \"workspace-write\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readback := func(client *codex.Client, want types.SandboxMode) {
		t.Helper()
		cfg, err := client.ReadConfig(ctx)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if cfg.Sandbox == nil || *cfg.Sandbox != string(want) || string(cfg.Raw["sandbox_mode"]) != fmt.Sprintf("%q", want) {
			t.Fatalf("effective sandbox default differs from %q", want)
		}
	}
	client := connect0171(t, ctx, opts)
	readback(client, types.SandboxWorkspaceWrite)
	if err := client.SetSandbox(ctx, types.SandboxReadOnly); err != nil {
		t.Fatalf("set sandbox default: %v", err)
	}
	readback(client, types.SandboxReadOnly)
	if err := client.Close(ctx); err != nil {
		t.Fatalf("close original app-server: %v", err)
	}
	readback(connect0171(t, ctx, opts), types.SandboxReadOnly)
	t.Log("ReadConfig matched the typed and raw sandbox_mode before writing, after writing, and after restart")
}

type mcp0171Request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ProtocolVersion string `json:"protocolVersion"`
	} `json:"params"`
}

func mcp0171Reply(req mcp0171Request, tool string) map[string]any {
	if len(req.ID) == 0 {
		return nil
	}
	response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	switch req.Method {
	case "initialize":
		response["result"] = map[string]any{
			"protocolVersion": req.Params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": tool, "version": "1"},
		}
	case "tools/list":
		response["result"] = map[string]any{"tools": []map[string]any{{
			"name": tool, "inputSchema": map[string]string{"type": "object"},
		}}}
	case "ping":
		response["result"] = map[string]any{}
	default:
		response["error"] = map[string]any{"code": -32601, "message": "unsupported fixture method"}
	}
	return response
}

// The test binary doubles as the inert stdio MCP process. Its stdout carries
// only protocol lines when the child invocation is selected.
func TestCodex0171MCPStdioFixture(t *testing.T) {
	if os.Getenv("CODEX_SDK_0171_MCP_CHILD") != "1" {
		t.Skip("stdio subprocess entry point")
	}
	if os.Getenv("CODEX_SDK_0171_MCP_SENTINEL") != mcp0171StdioValue {
		os.Exit(1)
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request mcp0171Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(1)
		}
		if reply := mcp0171Reply(request, "sdk_stdio_sentinel"); reply != nil {
			if writer.Encode(reply) != nil {
				os.Exit(1)
			}
		}
	}
	if scanner.Err() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCodex0171MCPConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	opts := codex0171Options(t, ctx, t.TempDir(), t.TempDir(), "CODEX_SDK_0171_FAKE_BEARER="+mcp0171FakeBearer)
	httpFixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Method != http.MethodPost {
			http.Error(w, "unsupported fixture request", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-SDK-Fixture") != mcp0171HTTPValue || r.Header.Get("Authorization") != "Bearer "+mcp0171FakeBearer {
			http.Error(w, "fixture credentials missing", http.StatusUnauthorized)
			return
		}
		var request mcp0171Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		reply := mcp0171Reply(request, "sdk_http_sentinel")
		if reply == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, reply)
	}))
	t.Cleanup(httpFixture.Close)

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	opts.WithMCPServers(map[string]types.McpServerConfig{
		"sdk-stdio": types.McpStdioConfig{
			Command: binary,
			Args:    []string{"-test.run=^TestCodex0171MCPStdioFixture$"},
			EnvironmentVariables: map[string]string{
				"CODEX_SDK_0171_MCP_CHILD": "1", "CODEX_SDK_0171_MCP_SENTINEL": mcp0171StdioValue,
			},
			ToolApprovalMode: types.MCPToolApprovalPrompt,
		},
		"sdk-http": types.McpStreamableHTTPConfig{
			URL: httpFixture.URL + "/mcp", BearerTokenEnvVar: "CODEX_SDK_0171_FAKE_BEARER",
			Headers: map[string]string{"X-SDK-Fixture": mcp0171HTTPValue},
		},
	})
	client := connect0171(t, ctx, opts)
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatalf("start thread with example MCP settings: %v", err)
	}
	for _, fixture := range []struct{ name, tool string }{
		{"sdk-stdio", "sdk_stdio_sentinel"},
		{"sdk-http", "sdk_http_sentinel"},
	} {
		page, err := client.ListMCPServerStatusPage(ctx, &types.MCPServerStatusListOptions{
			ThreadID: thread.ID(), ServerName: fixture.name,
		})
		if err != nil {
			t.Fatalf("discover %s: %v", fixture.name, err)
		}
		if len(page.Data) != 1 || page.Data[0].Name != fixture.name || page.Data[0].Tools[fixture.tool] == nil {
			t.Fatalf("%s did not advertise its sentinel tool", fixture.name)
		}
		t.Logf("%s advertised its sentinel tool", fixture.name)
	}
}

func stageCodex0171Auth(t *testing.T, from, home string) {
	t.Helper()
	source, err := os.Open(from)
	if err != nil {
		t.Fatal("cannot open the opt-in Codex auth file")
	}
	defer source.Close()
	dest, err := os.OpenFile(filepath.Join(home, "auth.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal("cannot stage the Codex auth file in the disposable home")
	}
	_, copyErr := io.Copy(dest, source)
	if closeErr := dest.Close(); copyErr != nil || closeErr != nil {
		t.Fatal("cannot stage the complete Codex auth file in the disposable home")
	}
}

func TestCodex0171ApprovedPlanHandoff(t *testing.T) {
	authFile := os.Getenv("CODEX_SDK_0171_AUTH_FILE")
	if authFile == "" {
		t.Skip("set CODEX_SDK_0171_AUTH_FILE to opt into a benign authenticated handoff")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	home, workspace := t.TempDir(), t.TempDir()
	opts := codex0171Options(t, ctx, home, workspace).WithExperimentalAPI(true)
	stageCodex0171Auth(t, authFile, home)

	client := connect0171(t, ctx, opts)
	original, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatalf("start original thread: %v", err)
	}
	destination, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatalf("start destination thread: %v", err)
	}
	if destination.ID() == original.ID() {
		t.Fatal("handoff requires a distinct destination")
	}
	const marker = "SDK_APPROVED_PLAN_OK"
	prepared, err := destination.PrepareApprovedPlan(ctx,
		"# Approved plan\nReply with exactly "+marker+". Do not use tools or change files.\n", nil)
	if err != nil {
		t.Fatalf("prepare approved plan: %v", err)
	}
	turn, err := destination.Run(ctx, prepared.Prompt, nil)
	if err != nil {
		t.Fatalf("run prepared prompt: %v", err)
	}
	if turn.ThreadID != destination.ID() || turn.Status != "completed" || strings.TrimSpace(turn.FinalResponse) != marker {
		t.Fatal("destination did not complete the approved plan with its marker")
	}
	for _, item := range turn.Items {
		switch item.(type) {
		case *types.CommandExecution, *types.FileChange, *types.MCPToolCall, *types.DynamicToolCall:
			t.Fatalf("handoff used an unexpected tool item %T", item)
		}
	}
	t.Log("fresh destination completed the prepared no-tool prompt once")
}
