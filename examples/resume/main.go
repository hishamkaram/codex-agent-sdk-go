// resume demonstrates session persistence across process restarts.
//
// First run (no args): starts a fresh thread, runs one turn, prints the
// thread ID. Save it.
//
// Second run (go run ./examples/resume <thread-id>): resumes the saved
// thread, runs another turn continuing the conversation.
//
// Resume uses thread/resume with an optional cwd override.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cwd, _ := os.Getwd()
	opts := types.NewCodexOptions().WithCwd(cwd)
	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close(context.Background())

	var thread *codex.Thread
	if len(os.Args) > 1 {
		id := os.Args[1]
		thread, err = client.ResumeThread(ctx, id, &types.ResumeOptions{Cwd: cwd})
		if err != nil {
			log.Fatalf("resume %s: %v", id, err)
		}
		fmt.Println("resumed:", thread.ID())
	} else {
		thread, err = client.StartThread(ctx, nil)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("started:", thread.ID())
		fmt.Println("save this id, then re-run with: go run ./examples/resume", thread.ID())
	}

	prompt := "Introduce yourself in one sentence."
	if len(os.Args) > 1 {
		prompt = "What did I just ask you?"
	}
	turn, err := thread.Run(ctx, prompt, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n" + turn.FinalResponse)
}
