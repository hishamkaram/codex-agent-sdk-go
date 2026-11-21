package threadconfig

import "github.com/hishamkaram/codex-agent-sdk-go/types"

type granularApprovalPolicyWireValue struct {
	Granular types.GranularApprovalSettings `json:"granular"`
}

// EncodeApprovalPolicy formats a policy for thread creation and config writes.
func EncodeApprovalPolicy(policy types.ApprovalPolicy) any {
	if policy == types.ApprovalGranular {
		return granularApprovalPolicyWireValue{
			Granular: types.DefaultGranularApprovalSettings(),
		}
	}
	return string(policy)
}
