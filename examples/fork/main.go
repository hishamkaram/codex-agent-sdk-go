// fork demonstrates branching a thread. The original thread is unchanged;
// the branch continues independently with the original's history as
// context.
//
// Run: go run ./examples/fork
//
// Requires that thread/fork is supported by the target codex CLI version.
// The SDK surfaces the server's error as a types.RPCError if it isn't.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := codex.NewClient(ctx, types.NewCodexOptions())
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close(context.Background())

	original, err := client.StartThread(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := original.Run(ctx, "Let's say my favorite color is blue.", nil); err != nil {
		log.Fatal(err)
	}

	branch, fork, err := client.ForkThread(ctx, original.ID(), nil)
	if err != nil {
		var rpc *types.RPCError
		if errors.As(err, &rpc) {
			log.Fatalf("fork not supported by this codex CLI: %v", rpc)
		}
		log.Fatal(err)
	}
	fmt.Printf("forked %s → %s\n", fork.SourceThreadID, fork.NewThreadID)

	turn, err := branch.Run(ctx, "What did I say my favorite color was?", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("branch:", turn.FinalResponse)
}
