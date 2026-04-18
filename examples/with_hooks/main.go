// with_hooks demonstrates hook observer mode.
//
// Requires: ~/.codex/hooks.json configured with at least one hook handler
// (otherwise no hook events fire). See docs/hooks.md for the DIY setup.
//
// Run: go run ./examples/with_hooks
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

	// Tier 1: observer mode — receives hook/started and hook/completed
	// notifications whenever codex fires a hook from the user's
	// ~/.codex/hooks.json. No callback setup required.
	opts := types.NewCodexOptions().WithHooks(true)

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
	fmt.Println("thread:", thread.ID())

	events, err := thread.RunStreamed(ctx, "Print exactly: hook demo", nil)
	if err != nil {
		log.Fatal(err)
	}
	for ev := range events {
		switch e := ev.(type) {
		case *types.HookStarted:
			fmt.Printf("[hook started] event=%s handler=%s scope=%s source=%s\n",
				e.Run.EventName, e.Run.HandlerType, e.Run.Scope, e.Run.SourcePath)
		case *types.HookCompleted:
			dur := "?"
			if e.Run.DurationMs != nil {
				dur = fmt.Sprintf("%dms", *e.Run.DurationMs)
			}
			fmt.Printf("[hook done   ] event=%s status=%s duration=%s entries=%d\n",
				e.Run.EventName, e.Run.Status, dur, len(e.Run.Entries))
		case *types.ItemCompleted:
			if msg, ok := e.Item.(*types.AgentMessage); ok {
				fmt.Println("\n>>>", msg.Text)
			}
		case *types.TurnCompleted:
			fmt.Printf("\ntokens: in=%d out=%d\n",
				e.Usage.InputTokens, e.Usage.OutputTokens)
		}
	}
}
