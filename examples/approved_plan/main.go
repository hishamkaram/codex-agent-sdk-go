// approved_plan prepares human-approved Markdown for a fresh implementation
// thread while keeping the planning thread available. Supply the absolute
// path to a native Linux Codex executable, the original thread ID, and a
// file containing the approved Markdown.
//
// Run with: go run ./examples/approved_plan -cli /path/to/codex -source-thread THREAD_ID -plan approved.md
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func main() {
	cli := flag.String("cli", "", "absolute path to the native Codex executable")
	source := flag.String("source-thread", "", "ID of the existing planning thread")
	plan := flag.String("plan", "", "path to the approved Markdown file")
	flag.Parse()

	if !filepath.IsAbs(*cli) || strings.TrimSpace(*source) == "" || *plan == "" {
		log.Fatal("supply -cli, -source-thread and -plan")
	}
	markdown, err := os.ReadFile(*plan)
	if err != nil {
		log.Fatal(err)
	}
	if err := handoff(*cli, *source, string(markdown)); err != nil {
		log.Fatal(err)
	}
}

func handoff(cliPath, sourceID, markdown string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := codex.NewClient(ctx, types.NewCodexOptions().WithCLIPath(cliPath).WithExperimentalAPI(true))
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if closeErr := client.Close(closeCtx); closeErr != nil {
			log.Printf("close client: %v", closeErr)
		}
	}()
	if connectErr := client.Connect(ctx); connectErr != nil {
		return connectErr
	}

	// Keep the planning thread ID for the user. Only the new destination
	// receives preparation and its first turn.
	destination, err := client.StartThread(ctx, nil)
	if err != nil {
		return fmt.Errorf("start implementation thread for source %s: %w", sourceID, err)
	}
	prepared, err := destination.PrepareApprovedPlan(ctx, markdown, nil)
	if err != nil {
		return fmt.Errorf("prepare destination %s for source %s: %w", destination.ID(), sourceID, err)
	}
	turn, err := destination.Run(ctx, prepared.Prompt, nil)
	if err != nil {
		return fmt.Errorf("run destination %s for source %s: %w", destination.ID(), sourceID, err)
	}

	fmt.Printf("Planning thread %s. Implementation thread %s.\n%s\n", sourceID, destination.ID(), turn.FinalResponse)
	return nil
}
