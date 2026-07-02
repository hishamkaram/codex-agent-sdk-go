package types

import (
	"encoding/json"
	"testing"
)

func TestConfigUnmarshal_ApprovalPolicyGranularObject(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"model": "gpt-5.4",
		"model_reasoning_effort": "xhigh",
		"approval_policy": {
			"granular": {
				"mcp_elicitations": true,
				"rules": true,
				"sandbox_approval": true
			}
		},
		"sandbox": "workspace-write"
	}`)
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Unmarshal Config: %v", err)
	}
	if cfg.ApprovalPolicy == nil || *cfg.ApprovalPolicy != string(ApprovalGranular) {
		t.Fatalf("ApprovalPolicy = %v, want granular", cfg.ApprovalPolicy)
	}
	if cfg.ModelReasoningEffort == nil || *cfg.ModelReasoningEffort != "xhigh" {
		t.Fatalf("ModelReasoningEffort = %v, want xhigh", cfg.ModelReasoningEffort)
	}
	if cfg.Raw == nil {
		t.Fatal("Raw is nil")
	}
	if _, ok := cfg.Raw["model_reasoning_effort"]; !ok {
		t.Fatal("Raw missing model_reasoning_effort")
	}
	var preserved map[string]json.RawMessage
	if err := json.Unmarshal(cfg.Raw["approval_policy"], &preserved); err != nil {
		t.Fatalf("Raw approval_policy = %s, want object: %v", cfg.Raw["approval_policy"], err)
	}
	if _, ok := preserved["granular"]; !ok {
		t.Fatalf("Raw approval_policy = %s, want granular key", cfg.Raw["approval_policy"])
	}
}

func TestConfigUnmarshal_ApprovalPolicyStringAndNull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		wantNil  bool
		wantMode string
	}{
		{
			name:     "string",
			raw:      `{"approval_policy":"on-request"}`,
			wantMode: string(ApprovalOnRequest),
		},
		{
			name:    "null",
			raw:     `{"approval_policy":null}`,
			wantNil: true,
		},
		{
			name:    "missing",
			raw:     `{}`,
			wantNil: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg Config
			if err := json.Unmarshal([]byte(tt.raw), &cfg); err != nil {
				t.Fatalf("Unmarshal Config: %v", err)
			}
			if tt.wantNil {
				if cfg.ApprovalPolicy != nil {
					t.Fatalf("ApprovalPolicy = %q, want nil", *cfg.ApprovalPolicy)
				}
				return
			}
			if cfg.ApprovalPolicy == nil || *cfg.ApprovalPolicy != tt.wantMode {
				t.Fatalf("ApprovalPolicy = %v, want %q", cfg.ApprovalPolicy, tt.wantMode)
			}
		})
	}
}
