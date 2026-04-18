package types

import (
	"context"
	"testing"
)

func TestNewCodexOptions_Defaults(t *testing.T) {
	t.Parallel()
	o := NewCodexOptions()
	if o.DefaultSandbox != SandboxReadOnly {
		t.Fatalf("default sandbox = %q", o.DefaultSandbox)
	}
	if o.DefaultApprovalPolicy != ApprovalOnRequest {
		t.Fatalf("default approval = %q", o.DefaultApprovalPolicy)
	}
	if o.ClientName == "" || o.ClientVersion == "" {
		t.Fatal("client info should have defaults")
	}
}

func TestCodexOptions_ChainableWithMethods(t *testing.T) {
	t.Parallel()
	approvals := func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalAccept{}
	}
	o := NewCodexOptions().
		WithCLIPath("/bin/codex").
		WithExtraArgs("--verbose").
		WithEnv("KEY=VAL").
		WithReadBufferSize(4*1024*1024).
		WithVerbose(true).
		WithClientInfo("test-client", "9.9", "Test").
		WithModel("gpt-5.4").
		WithCwd("/work").
		WithSandbox(SandboxWorkspaceWrite).
		WithApprovalPolicy(ApprovalUntrusted).
		WithApprovalCallback(approvals)

	if o.CLIPath != "/bin/codex" {
		t.Fatal(o.CLIPath)
	}
	if len(o.ExtraArgs) != 1 || o.ExtraArgs[0] != "--verbose" {
		t.Fatal(o.ExtraArgs)
	}
	if len(o.Env) != 1 || o.Env[0] != "KEY=VAL" {
		t.Fatal(o.Env)
	}
	if o.ReadBufferSize != 4*1024*1024 || !o.Verbose {
		t.Fatal("buffer/verbose not set")
	}
	if o.ClientName != "test-client" || o.ClientVersion != "9.9" || o.ClientTitle != "Test" {
		t.Fatal("client info")
	}
	if o.DefaultModel != "gpt-5.4" || o.DefaultCwd != "/work" {
		t.Fatal("model/cwd")
	}
	if o.DefaultSandbox != SandboxWorkspaceWrite || o.DefaultApprovalPolicy != ApprovalUntrusted {
		t.Fatal("policy override")
	}
	if o.ApprovalCallback == nil {
		t.Fatal("approval callback not set")
	}
}

func TestCodexOptions_WithMCPServers(t *testing.T) {
	t.Parallel()
	servers := map[string]McpServerConfig{
		"stdio": McpStdioConfig{Command: "npx", Args: []string{"srv"}},
		"http":  McpStreamableHTTPConfig{URL: "https://mcp.example.com", AuthType: "bearer"},
	}
	o := NewCodexOptions().WithMCPServers(servers)
	if len(o.DefaultMCPServers) != 2 {
		t.Fatalf("len = %d", len(o.DefaultMCPServers))
	}
	if o.DefaultMCPServers["stdio"].Kind() != "stdio" {
		t.Fatal("kind(stdio)")
	}
	if o.DefaultMCPServers["http"].Kind() != "http" {
		t.Fatal("kind(http)")
	}
}
