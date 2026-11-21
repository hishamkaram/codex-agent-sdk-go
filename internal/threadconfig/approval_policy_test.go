package threadconfig

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestEncodeApprovalPolicy_GranularUsesStructuredObject(t *testing.T) {
	t.Parallel()

	assertGranularApprovalPolicyValue(t, EncodeApprovalPolicy(types.ApprovalGranular))
}

func TestEncodeApprovalPolicy_NonGranularPoliciesRemainStrings(t *testing.T) {
	t.Parallel()

	tests := []types.ApprovalPolicy{
		types.ApprovalUntrusted,
		types.ApprovalPolicy("on-failure"),
		types.ApprovalOnRequest,
		types.ApprovalNever,
	}
	for _, policy := range tests {
		policy := policy
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			got := EncodeApprovalPolicy(policy)
			if got != string(policy) {
				t.Fatalf("EncodeApprovalPolicy(%q) = %#v, want string %q", policy, got, policy)
			}
		})
	}
}

func assertGranularApprovalPolicyValue(t *testing.T, got any) {
	t.Helper()

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal approval policy: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("approval policy JSON = %s, want object: %v", raw, err)
	}
	if len(top) != 1 {
		t.Fatalf("approval policy JSON = %s, want one top-level granular key", raw)
	}
	granularRaw, ok := top["granular"]
	if !ok {
		t.Fatalf("approval policy JSON = %s, want granular object", raw)
	}
	var granular map[string]bool
	if err := json.Unmarshal(granularRaw, &granular); err != nil {
		t.Fatalf("granular JSON = %s, want bool map: %v", granularRaw, err)
	}
	want := map[string]bool{
		"mcp_elicitations": true,
		"rules":            true,
		"sandbox_approval": true,
	}
	if !reflect.DeepEqual(granular, want) {
		t.Fatalf("granular = %#v, want %#v", granular, want)
	}
}
