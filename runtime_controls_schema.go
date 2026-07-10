package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: read app-server schema: %w", err)
	}
	var schema threadStartSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: decode app-server schema: %w", err)
	}
	approval, ok := schema.Definitions["AskForApproval"]
	if !ok {
		return nil, fmt.Errorf("codex.DiscoverRuntimeControls: app-server schema omitted AskForApproval")
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
