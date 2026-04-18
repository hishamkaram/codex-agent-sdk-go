// with_mcp demonstrates registering MCP (Model Context Protocol) servers
// so the codex agent can call external tools. Two transports are shown:
// stdio (local subprocess speaking MCP) and streamable HTTP (remote).
//
// Note: v0.1.0 delivers MCP config via CodexOptions.DefaultMCPServers;
// the SDK serializes this into the thread/start params (future versions
// may migrate to config/batchWrite). The fixture transcript does not
// cover this path, so per-server behavior is CLI-version dependent.
//
// Run: go run ./examples/with_mcp
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts := types.NewCodexOptions().
		WithSandbox(types.SandboxReadOnly).
		WithMCPServers(map[string]types.McpServerConfig{
			"fetch": types.McpStdioConfig{
				Command:             "npx",
				Args:                []string{"-y", "@modelcontextprotocol/server-fetch"},
				DefaultApprovalMode: types.ApprovalOnRequest,
			},
			"docs": types.McpStreamableHTTPConfig{
				URL:      "https://mcp.example.com",
				AuthType: "bearer",
				// BearerToken: os.Getenv("DOCS_MCP_TOKEN"),
			},
		})

	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close(context.Background())

	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}

	turn, err := thread.Run(ctx,
		"Use the fetch MCP tool to get https://example.com/ and summarize.",
		nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(turn.FinalResponse)
	for _, it := range turn.Items {
		if tc, ok := it.(*types.MCPToolCall); ok {
			fmt.Printf("  mcp: %s::%s (%s)\n", tc.ServerName, tc.ToolName, tc.Status)
		}
	}
}
