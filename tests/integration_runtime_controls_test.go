//go:build integration

package tests

import (
	"context"
	"testing"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestIntegration_RuntimeControlDiscovery(t *testing.T) {
	requireCodex(t)
	controls, err := codex.DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions(),
		nil,
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeControls: %v", err)
	}
	if controls.CLIVersion == "" || len(controls.ApprovalPolicies) == 0 || len(controls.SandboxModes) == 0 {
		t.Fatalf("incomplete runtime controls: %+v", controls)
	}
	for _, option := range append(controls.ApprovalPolicies, controls.SandboxModes...) {
		if option.ProviderValue == "" {
			t.Fatalf("empty provider control: %+v", controls)
		}
	}
}

func TestIntegration_ExperimentalRuntimeControlDiscovery(t *testing.T) {
	requireCodex(t)
	controls, err := codex.DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions().WithExperimentalAPI(true),
		nil,
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeControls experimental: %v", err)
	}
	for _, option := range controls.ApprovalPolicies {
		if option.ProviderValue == string(types.ApprovalGranular) {
			return
		}
	}
	t.Fatalf("installed CLI schema omitted granular approval: %+v", controls.ApprovalPolicies)
}

func TestIntegration_ConfigRequirementsConstrainRuntimeControls(t *testing.T) {
	client := connectReadOnlyClient(t)
	requirements, err := client.ReadConfigRequirements(context.Background())
	if err != nil {
		t.Fatalf("ReadConfigRequirements: %v", err)
	}
	controls, err := codex.DiscoverRuntimeControls(
		context.Background(),
		types.NewCodexOptions(),
		requirements,
	)
	if err != nil {
		t.Fatalf("DiscoverRuntimeControls with requirements: %v", err)
	}
	if len(controls.ApprovalPolicies) == 0 || len(controls.SandboxModes) == 0 {
		t.Fatalf("managed requirements removed every runtime control: %+v", controls)
	}
}
