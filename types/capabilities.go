package types

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RuntimeControlOption is one provider-native value advertised by the
// installed Codex CLI. Callers decide how, or whether, to map it into their UI.
type RuntimeControlOption struct {
	ProviderValue string `json:"provider_value"`
}

// RuntimeControlCapabilities contains runtime controls discovered from the
// installed CLI after managed requirements have been applied.
type RuntimeControlCapabilities struct {
	ApprovalPolicies []RuntimeControlOption `json:"approval_policies"`
	SandboxModes     []RuntimeControlOption `json:"sandbox_modes"`
	CLIVersion       string                 `json:"cli_version,omitempty"`
}

// RuntimeFeatureCapabilities contains provider surfaces proven from the
// installed app-server's generated JSON schemas. Callers must require the
// specific field they use; CLI version strings are informational only.
type RuntimeFeatureCapabilities struct {
	SubAgentActivity            bool   `json:"sub_agent_activity"`
	TurnInterrupt               bool   `json:"turn_interrupt"`
	BackgroundTerminalInventory bool   `json:"background_terminal_inventory"`
	BackgroundTerminalTerminate bool   `json:"background_terminal_terminate"`
	BackgroundTerminalsClean    bool   `json:"background_terminals_clean"`
	CLIVersion                  string `json:"cli_version,omitempty"`
}

// ConfigRequirementsReadResult is returned by configRequirements/read.
type ConfigRequirementsReadResult struct {
	Requirements *ConfigRequirements `json:"requirements"`
}

// ConfigRequirements contains the provider-managed allowlists that affect
// session startup controls. Nil slices mean the provider did not constrain the
// corresponding control.
type ConfigRequirements struct {
	AllowedApprovalPolicies []ApprovalPolicyRequirement `json:"allowedApprovalPolicies"`
	AllowedSandboxModes     []string                    `json:"allowedSandboxModes"`
}

// GranularApprovalSettings is Codex's structured granular approval policy.
// Optional false-valued fields are omitted on the wire and use provider
// defaults.
type GranularApprovalSettings struct {
	MCPElicitations    bool `json:"mcp_elicitations"`
	Rules              bool `json:"rules"`
	SandboxApproval    bool `json:"sandbox_approval"`
	SkillApproval      bool `json:"skill_approval,omitempty"`
	RequestPermissions bool `json:"request_permissions,omitempty"`
}

// DefaultGranularApprovalSettings returns the settings encoded by
// ApprovalGranular.
func DefaultGranularApprovalSettings() GranularApprovalSettings {
	return GranularApprovalSettings{
		MCPElicitations: true,
		Rules:           true,
		SandboxApproval: true,
	}
}

// ApprovalPolicyRequirement preserves both scalar policies and Codex's
// structured granular policy form.
type ApprovalPolicyRequirement struct {
	ProviderValue string
	Granular      *GranularApprovalSettings
}

// NewApprovalPolicyRequirement constructs a scalar provider requirement.
func NewApprovalPolicyRequirement(value string) ApprovalPolicyRequirement {
	return ApprovalPolicyRequirement{ProviderValue: value}
}

// MarshalJSON preserves the provider's scalar or structured requirement.
func (r ApprovalPolicyRequirement) MarshalJSON() ([]byte, error) {
	if r.Granular != nil {
		return json.Marshal(struct {
			Granular *GranularApprovalSettings `json:"granular"`
		}{Granular: r.Granular})
	}
	return json.Marshal(r.ProviderValue)
}

func (r *ApprovalPolicyRequirement) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	var value string
	if err := json.Unmarshal(trimmed, &value); err == nil {
		*r = NewApprovalPolicyRequirement(value)
		return nil
	}
	var object struct {
		Granular *GranularApprovalSettings `json:"granular"`
	}
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return fmt.Errorf("types.ApprovalPolicyRequirement: decode: %w", err)
	}
	if object.Granular == nil {
		return fmt.Errorf("types.ApprovalPolicyRequirement: unsupported object")
	}
	*r = ApprovalPolicyRequirement{
		ProviderValue: string(ApprovalGranular),
		Granular:      object.Granular,
	}
	return nil
}
