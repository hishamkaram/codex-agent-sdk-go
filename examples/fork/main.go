// fork demonstrates branching a thread. The original thread is unchanged;
// the branch continues independently with the original's history as
// context.
//
// Run: go run ./examples/fork
//
// The SDK sends the parent's ID as threadId in the app-server fork request
// and surfaces any server rejection as a types.RPCError.
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
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := codex.NewClient(ctx, types.NewCodexOptions())
	if err != nil {
		return err
	}
	if connErr := client.Connect(ctx); connErr != nil {
		return connErr
	}
	defer func() {
		if closeErr := client.Close(context.Background()); closeErr != nil {
			log.Printf("close client: %v", closeErr)
		}
	}()

	original, err := client.StartThread(ctx, nil)
	if err != nil {
		return err
	}
	if _, runErr := original.Run(ctx, "Let's say my favorite color is blue.", nil); runErr != nil {
		return runErr
	}

	branch, fork, err := client.ForkThread(ctx, original.ID(), nil)
	if err != nil {
		return fmt.Errorf("fork thread: %w", err)
	}
	fmt.Printf("forked %s → %s\n", fork.SourceThreadID, fork.NewThreadID)

	turn, err := branch.Run(ctx, "What did I say my favorite color was?", nil)
	if err != nil {
		return err
	}
	fmt.Println("branch:", turn.FinalResponse)
	return nil
}
