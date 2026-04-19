package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// defaultAgentsMDTemplate is the scaffold Client.InitAgentsMD writes
// when no Template override is supplied. Mirrors the content codex's
// TUI `/init` slash command generates.
const defaultAgentsMDTemplate = `# AGENTS.md

## Project Overview

<!-- Describe the repo in 1-3 sentences. What does it do? What's the tech stack? -->

## Build & Test

<!-- Commands a contributor runs to verify changes. Example:

	go test ./...
	npm run lint
-->

## Conventions

<!-- House style, naming rules, module boundaries, anything an agent
     should know before editing. -->

## Do Not

<!-- Things that look like good ideas but break the build / policy. -->
`

// InitAgentsMD writes an AGENTS.md scaffold into the given directory.
// Equivalent to TUI `/init`. The scaffold is a hardcoded template
// (override via InitAgentsMDOptions.Template).
//
// Behavior:
//   - If dir does not exist → returns wrapped *os.PathError
//   - If dir/AGENTS.md exists AND opts.Overwrite is false →
//     returns *types.AGENTSMDExistsError
//   - If dir/AGENTS.md exists AND opts.Overwrite is true → overwrite
//   - Otherwise: write template, return nil
//
// The file is written with 0644 permissions.
//
// InitAgentsMD is a LOCAL helper — it does NOT reach the wire. The
// ctx parameter is accepted for consistency but is not consulted;
// file writes are fast enough to not need cancellation.
func (c *Client) InitAgentsMD(ctx context.Context, dir string, opts *types.InitAgentsMDOptions) error {
	_ = ctx // local file write; ctx unused intentionally
	if dir == "" {
		return fmt.Errorf("codex.Client.InitAgentsMD: dir must not be empty")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("codex.Client.InitAgentsMD: stat dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("codex.Client.InitAgentsMD: %q is not a directory", dir)
	}

	target := filepath.Join(dir, "AGENTS.md")
	overwrite := false
	template := defaultAgentsMDTemplate
	if opts != nil {
		overwrite = opts.Overwrite
		if opts.Template != "" {
			template = opts.Template
		}
	}

	if _, err := os.Stat(target); err == nil {
		if !overwrite {
			return types.NewAGENTSMDExistsError(target)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("codex.Client.InitAgentsMD: stat target: %w", err)
	}

	if err := os.WriteFile(target, []byte(template), 0o644); err != nil {
		return fmt.Errorf("codex.Client.InitAgentsMD: write: %w", err)
	}
	return nil
}

// GitDiffToRemote returns the diff between the current working
// directory and the remote tracking branch, as computed by codex.
// Distinct from Thread.GitDiff (which runs `git diff` locally against
// the working tree).
//
// Wire method: `gitDiffToRemote` — verified live against codex
// 0.121.0; requires an absolute `cwd` path, errors otherwise.
//
// This is a Client-level method (not Thread-level) because the cwd
// is a parameter, not an implicit thread property.
func (c *Client) GitDiffToRemote(ctx context.Context, cwd string) (*types.RemoteDiffResult, error) {
	if cwd == "" {
		return nil, fmt.Errorf("codex.Client.GitDiffToRemote: cwd must not be empty")
	}
	resp, err := c.sendRaw(ctx, "GitDiffToRemote", "gitDiffToRemote", types.RemoteDiffParams{Cwd: cwd})
	if err != nil {
		return nil, err
	}
	var out types.RemoteDiffResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("codex.Client.GitDiffToRemote: decode response: %w", err)
	}
	return &out, nil
}
