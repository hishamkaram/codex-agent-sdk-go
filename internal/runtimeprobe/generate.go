package runtimeprobe

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
	cli transport.CLI,
	unsupported error,
) ([]string, error) {
	outDir, cleanup, err := GenerateSchema(ctx, cli, "codex.DiscoverRuntimeControls", true, unsupported)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	raw, err := os.ReadFile(filepath.Join(outDir, "v2", "ThreadStartParams.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"codex.DiscoverRuntimeControls: app-server schema is unavailable: %w",
				unsupported,
			)
		}
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: read app-server schema: %w", err)
	}
	var schema threadStartSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf(
			"codex.DiscoverRuntimeControls: decode app-server schema: %w",
			errors.Join(unsupported, err),
		)
	}
	approval, ok := schema.Definitions["AskForApproval"]
	if !ok {
		return nil, fmt.Errorf(
			"codex.DiscoverRuntimeControls: app-server schema omitted AskForApproval: %w",
			unsupported,
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

// GenerateSchema generates a schema in a temporary directory the caller must release using cleanup.
func GenerateSchema(
	ctx context.Context,
	cli transport.CLI,
	caller string,
	experimental bool,
	unsupported error,
) (outDir string, cleanup func(), err error) {
	if checkErr := requireSchemaCommand(ctx, cli, caller, experimental, unsupported); checkErr != nil {
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
	_, _, runErr := cli.Run(ctx, runtimeControlCommandWaitDelay, args...)
	if runErr != nil {
		cleanup()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", func() {}, fmt.Errorf("%s: generate app-server schema: %w", caller, ctxErr)
		}
		return "", func() {}, fmt.Errorf("%s: generate app-server schema: %w", caller, runErr)
	}
	return outDir, cleanup, nil
}

func requireSchemaCommand(
	ctx context.Context,
	cli transport.CLI,
	caller string,
	experimental bool,
	unsupported error,
) error {
	stdout, stderr, err := cli.Run(
		ctx,
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
				errors.Join(unsupported, err),
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
				unsupported,
			)
		}
	}
	return nil
}
