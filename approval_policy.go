package codex

import "github.com/hishamkaram/codex-agent-sdk-go/types"

type granularApprovalPolicyWireValue struct {
	Granular types.GranularApprovalSettings `json:"granular"`
}

func encodeApprovalPolicy(policy types.ApprovalPolicy) any {
	if policy == types.ApprovalGranular {
		return granularApprovalPolicyWireValue{
			Granular: types.DefaultGranularApprovalSettings(),
		}
	}
	return string(policy)
}
