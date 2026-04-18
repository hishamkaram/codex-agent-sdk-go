// structured_output constrains the agent's final response to match a
// JSON Schema via RunOptions.OutputSchema. Useful for programmatic
// consumption — you parse the AgentMessage content as JSON directly.
//
// Run: go run ./examples/structured_output
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// RepoSummary is the target shape. The schema mirrors it so the model
// produces JSON the Go side can Unmarshal directly.
type RepoSummary struct {
	Language string `json:"language"`
	Purpose  string `json:"purpose"`
	Status   string `json:"status"` // "ok" | "needs_action"
}

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

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language": map[string]any{"type": "string"},
			"purpose":  map[string]any{"type": "string"},
			"status":   map[string]any{"enum": []string{"ok", "needs_action"}},
		},
		"required":             []string{"language", "purpose", "status"},
		"additionalProperties": false,
	}

	turn, err := thread.Run(ctx,
		"Summarize the current repository: language, one-line purpose, status.",
		&types.RunOptions{
			OutputSchema: &types.OutputSchema{
				Name:   "RepoSummary",
				Schema: schema,
				Strict: true,
			},
		})
	if err != nil {
		log.Fatal(err)
	}

	var summary RepoSummary
	if err := json.Unmarshal([]byte(turn.FinalResponse), &summary); err != nil {
		log.Fatalf("model returned non-conforming JSON %q: %v", turn.FinalResponse, err)
	}
	fmt.Printf("%+v\n", summary)
}
