package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type threadStartSchema struct {
	Definitions map[string]approvalPolicySchema `json:"definitions"`
}

type approvalPolicySchema struct {
	OneOf []approvalPolicyVariant `json:"oneOf"`
}

type approvalPolicyVariant struct {
	Enum       []string                   `json:"enum"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func discoverExperimentalApprovalPolicies(
	ctx context.Context,
	cliPath string,
	env []string,
) ([]string, error) {
	if err := requireExperimentalSchemaCommand(ctx, cliPath, env); err != nil {
		return nil, err
	}
	outDir, err := os.MkdirTemp("", "codex-app-server-schema-")
	if err != nil {
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: create schema directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(outDir) }()

	cmd := exec.CommandContext(
		ctx,
		cliPath,
		"app-server",
		"generate-json-schema",
		"--experimental",
		"--out",
		outDir,
	)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = transport.BuildRuntimeEnvironment(env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("codex.DiscoverRuntimeControls: generate app-server schema: %w", ctxErr)
		}
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: generate app-server schema: %w", runErr)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "v2", "ThreadStartParams.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"codex.DiscoverRuntimeControls: app-server schema is unavailable: %w",
				ErrRuntimeControlsUnsupported,
			)
		}
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: read app-server schema: %w", err)
	}
	var schema threadStartSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf(
			"codex.DiscoverRuntimeControls: decode app-server schema: %w",
			errors.Join(ErrRuntimeControlsUnsupported, err),
		)
	}
	approval, ok := schema.Definitions["AskForApproval"]
	if !ok {
		return nil, fmt.Errorf(
			"codex.DiscoverRuntimeControls: app-server schema omitted AskForApproval: %w",
			ErrRuntimeControlsUnsupported,
		)
	}

	var values []string
	for _, variant := range approval.OneOf {
		for _, value := range variant.Enum {
			values = appendUnique(values, value)
		}
		if _, supported := variant.Properties[string(types.ApprovalGranular)]; supported {
			values = appendUnique(values, string(types.ApprovalGranular))
		}
	}
	return values, nil
}

func requireExperimentalSchemaCommand(ctx context.Context, cliPath string, env []string) error {
	cmd := exec.CommandContext(ctx, cliPath, "app-server", "generate-json-schema", "--help")
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = transport.BuildRuntimeEnvironment(env)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("codex.DiscoverRuntimeControls: app-server help: %w", ctxErr)
		}
		missingCommand := strings.Contains(output.String(), "unexpected argument 'generate-json-schema' found") ||
			strings.Contains(output.String(), "unrecognized subcommand 'generate-json-schema'")
		if missingCommand {
			return fmt.Errorf(
				"codex.DiscoverRuntimeControls: app-server schema command is unavailable: %w",
				errors.Join(ErrRuntimeControlsUnsupported, err),
			)
		}
		return fmt.Errorf("codex.DiscoverRuntimeControls: app-server help: %w", err)
	}
	required := map[string]bool{"--experimental": false, "--out": false}
	for _, field := range strings.Fields(output.String()) {
		if _, ok := required[field]; ok {
			required[field] = true
		}
	}
	if required["--experimental"] && required["--out"] {
		return nil
	}
	return fmt.Errorf(
		"codex.DiscoverRuntimeControls: app-server does not advertise required schema options: %w",
		ErrRuntimeControlsUnsupported,
	)
}
