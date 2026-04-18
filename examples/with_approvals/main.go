// with_approvals demonstrates handling server-initiated approval requests.
//
// The approval policy "untrusted" means codex asks before every state-
// mutating command. The callback auto-approves `ls` and friends, denies
// anything else.
//
// Run: go run ./examples/with_approvals
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// safeCommand reports whether cmd is in the read-only allowlist.
func safeCommand(cmd string) bool {
	for _, safe := range []string{"ls", "pwd", "cat ", "head ", "tail ", "grep ", "find ", "wc "} {
		if strings.HasPrefix(cmd, safe) || cmd == strings.TrimSpace(safe) {
			return true
		}
	}
	return false
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cwd, _ := os.Getwd()
	opts := types.NewCodexOptions().
		WithCwd(cwd).
		WithSandbox(types.SandboxWorkspaceWrite).
		WithApprovalPolicy(types.ApprovalUntrusted).
		WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
			switch r := req.(type) {
			case *types.CommandExecutionApprovalRequest:
				if safeCommand(r.Command) {
					fmt.Fprintf(os.Stderr, "[auto-approve] %s\n", r.Command)
					return types.ApprovalAccept{}
				}
				fmt.Fprintf(os.Stderr, "[deny] %s\n", r.Command)
				return types.ApprovalDeny{Reason: "not on allowlist"}
			case *types.FileChangeApprovalRequest:
				fmt.Fprintf(os.Stderr, "[deny file %s] %s\n", r.Operation, r.Path)
				return types.ApprovalDeny{Reason: "read-only mode"}
			}
			return types.ApprovalDeny{Reason: "unsupported request type"}
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
	turn, err := thread.Run(ctx, "Run `ls` and summarize what files are here.", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(turn.FinalResponse)
}
