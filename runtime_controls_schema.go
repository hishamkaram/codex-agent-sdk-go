package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	outDir, cleanup, err := generateExperimentalSchema(ctx, cliPath, env, "codex.DiscoverRuntimeControls")
	if err != nil {
		return nil, err
	}
	defer cleanup()

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

func generateExperimentalSchema(
	ctx context.Context,
	cliPath string,
	env []string,
	caller string,
) (outDir string, cleanup func(), err error) {
	return generateAppServerSchema(ctx, cliPath, env, caller, true)
}

func generateAppServerSchema(
	ctx context.Context,
	cliPath string,
	env []string,
	caller string,
	experimental bool,
) (outDir string, cleanup func(), err error) {
	if checkErr := requireSchemaCommand(ctx, cliPath, env, caller, experimental); checkErr != nil {
		return "", func() {}, checkErr
	}
	outDir, err = os.MkdirTemp("", "codex-app-server-schema-")
	if err != nil {
		return "", func() {}, fmt.Errorf("%s: create schema directory: %w", caller, err)
	}
	cleanup = func() { _ = os.RemoveAll(outDir) }
	args := []string{"app-server", "generate-json-schema"}
	if experimental {
		args = append(args, "--experimental")
	}
	args = append(args, "--out", outDir)
	_, _, runErr := transport.RunCLICommand(ctx, cliPath, env, runtimeControlCommandWaitDelay, args...)
	if runErr != nil {
		cleanup()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", func() {}, fmt.Errorf("%s: generate app-server schema: %w", caller, ctxErr)
		}
		return "", func() {}, fmt.Errorf("%s: generate app-server schema: %w", caller, runErr)
	}
	return outDir, cleanup, nil
}

func requireExperimentalSchemaCommand(ctx context.Context, cliPath string, env []string) error {
	return requireSchemaCommand(ctx, cliPath, env, "codex.DiscoverRuntimeControls", true)
}

func requireSchemaCommand(
	ctx context.Context,
	cliPath string,
	env []string,
	caller string,
	experimental bool,
) error {
	stdout, stderr, err := transport.RunCLICommand(
		ctx,
		cliPath,
		env,
		runtimeControlCommandWaitDelay,
		"app-server",
		"generate-json-schema",
		"--help",
	)
	output := stdout + stderr
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: app-server help: %w", caller, ctxErr)
		}
		missingCommand := strings.Contains(output, "unexpected argument 'generate-json-schema' found") ||
			strings.Contains(output, "unrecognized subcommand 'generate-json-schema'")
		if missingCommand {
			return fmt.Errorf(
				"%s: app-server schema command is unavailable: %w",
				caller,
				errors.Join(ErrRuntimeControlsUnsupported, err),
			)
		}
		return fmt.Errorf("%s: app-server help: %w", caller, err)
	}
	required := map[string]bool{"--out": false}
	if experimental {
		required["--experimental"] = false
	}
	for _, field := range strings.Fields(output) {
		if _, ok := required[field]; ok {
			required[field] = true
		}
	}
	for _, found := range required {
		if !found {
			return fmt.Errorf(
				"%s: app-server does not advertise required schema options: %w",
				caller,
				ErrRuntimeControlsUnsupported,
			)
		}
	}
	return nil
}
