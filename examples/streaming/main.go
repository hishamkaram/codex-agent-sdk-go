// streaming demonstrates a persistent Client with two sequential turns on
// one Thread, printing streaming agent-message deltas as they arrive.
//
// Run: go run ./examples/streaming
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

	client, err := codex.NewClient(ctx, types.NewCodexOptions())
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
	fmt.Println("thread:", thread.ID())

	for i, prompt := range []string{
		"Think of a three-word motto for a Go SDK.",
		"Now shorten it to two words.",
	} {
		fmt.Printf("\n--- turn %d: %s ---\n", i+1, prompt)

		events, err := thread.RunStreamed(ctx, prompt, nil)
		if err != nil {
			log.Fatal(err)
		}
		for ev := range events {
			switch e := ev.(type) {
			case *types.ItemUpdated:
				if d, ok := e.Delta.(*types.AgentMessageDelta); ok {
					fmt.Print(d.TextChunk)
				}
			case *types.TurnCompleted:
				fmt.Printf("\n[tokens: in=%d out=%d]\n",
					e.Usage.InputTokens, e.Usage.OutputTokens)
			}
		}
	}
}
