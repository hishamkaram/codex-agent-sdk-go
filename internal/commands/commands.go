// Package commands builds provider command requests and decodes replies.
// Each call uses the connection supplied by the public client.
package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Send sends a command using the caller's connection and public RPC error policy.
type Send func(ctx context.Context, callerName, method string, params any) (json.RawMessage, error)

func GitDiffToRemote(ctx context.Context, send Send, cwd string) (*types.RemoteDiffResult, error) {
	if cwd == "" {
		return nil, fmt.Errorf("codex.Client.GitDiffToRemote: cwd must not be empty")
	}
	resp, err := send(ctx, "GitDiffToRemote", "gitDiffToRemote", types.RemoteDiffParams{Cwd: cwd})
	if err != nil {
		return nil, err
	}
	var out types.RemoteDiffResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("codex.Client.GitDiffToRemote: decode response: %w", err)
	}
	return &out, nil
}
