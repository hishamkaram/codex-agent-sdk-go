package codex

import "github.com/hishamkaram/codex-agent-sdk-go/types"

type granularApprovalPolicyWireValue struct {
	Granular granularApprovalPolicySettings `json:"granular"`
}

type granularApprovalPolicySettings struct {
	MCPElicitations bool `json:"mcp_elicitations"`
	Rules           bool `json:"rules"`
	SandboxApproval bool `json:"sandbox_approval"`
}

func encodeApprovalPolicy(policy types.ApprovalPolicy) any {
	if policy == types.ApprovalGranular {
		return granularApprovalPolicyWireValue{
			Granular: granularApprovalPolicySettings{
				MCPElicitations: true,
				Rules:           true,
				SandboxApproval: true,
			},
		}
	}
	return string(policy)
}
