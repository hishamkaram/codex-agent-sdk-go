// simple_query is the minimal fire-and-forget example: one prompt, one
// turn, print the agent's final message, exit. The Codex client and
// subprocess are created and torn down automatically.
//
// Run: go run ./examples/simple_query
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func main() {
	ctx := context.Background()

	opts := types.NewCodexOptions().
		WithSandbox(types.SandboxReadOnly).
		WithApprovalPolicy(types.ApprovalOnRequest)

	events, err := codex.Query(ctx, "Reply with exactly: OK", opts)
	if err != nil {
		log.Fatalf("query failed: %v", err)
	}

	for ev := range events {
		if c, ok := ev.(*types.ItemCompleted); ok {
			if msg, ok := c.Item.(*types.AgentMessage); ok {
				fmt.Println(msg.Text)
			}
		}
		if tc, ok := ev.(*types.TurnCompleted); ok {
			fmt.Fprintf(os.Stderr, "tokens: in=%d out=%d\n",
				tc.Usage.InputTokens, tc.Usage.OutputTokens)
		}
	}
}
