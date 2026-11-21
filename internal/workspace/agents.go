package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// defaultAgentsMDTemplate is the scaffold InitAgentsMD writes
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

// InitAgentsMD writes the workspace's AGENTS.md scaffold.
func InitAgentsMD(dir string, opts *types.InitAgentsMDOptions) error {
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
