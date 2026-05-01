package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ParseApprovalRequest translates a server-initiated JSON-RPC request into
// a typed types.ApprovalRequest. Unrecognized methods return a
// *types.UnknownApprovalRequest.
func ParseApprovalRequest(method string, params json.RawMessage) (types.ApprovalRequest, error) {
	switch method {
	case "item/commandExecution/requestApproval":
		var r types.CommandExecutionApprovalRequest
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, wrapParseErr(method, params, err)
		}
		return &r, nil
	case "item/fileChange/requestApproval":
		var r types.FileChangeApprovalRequest
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, wrapParseErr(method, params, err)
		}
		return &r, nil
	case "item/permissions/requestApproval":
		var r types.PermissionsApprovalRequest
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, wrapParseErr(method, params, err)
		}
		return &r, nil
	case "mcpServer/elicitation/request":
		var r types.ElicitationRequest
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, wrapParseErr(method, params, err)
		}
		return &r, nil
	case "item/tool/requestUserInput":
		var r types.ToolRequestUserInputRequest
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, wrapParseErr(method, params, err)
		}
		return &r, nil
	default:
		cp := make(json.RawMessage, len(params))
		copy(cp, params)
		return &types.UnknownApprovalRequest{Method: method, Params: cp}, nil
	}
}

// EncodeApprovalDecision serializes a caller's ApprovalDecision into the
// wire shape accepted by the codex server in the response to a
// server-initiated approval request.
//
// Wire shape: {"decision": "accept" | "acceptForSession" | "decline" | "cancel", "reason"?: "..."}.
func EncodeApprovalDecision(d types.ApprovalDecision) map[string]any {
	switch dec := d.(type) {
	case types.ApprovalAccept:
		return map[string]any{"decision": "accept"}
	case types.ApprovalAcceptForSession:
		return map[string]any{"decision": "acceptForSession"}
	case types.ApprovalDeny:
		m := map[string]any{"decision": "decline"}
		if dec.Reason != "" {
			m["reason"] = dec.Reason
		}
		return m
	case types.ApprovalCancel:
		m := map[string]any{"decision": "cancel"}
		if dec.Reason != "" {
			m["reason"] = dec.Reason
		}
		return m
	case types.ToolRequestUserInputResponse:
		return map[string]any{"answers": normalizeUserInputAnswers(dec.Answers)}
	case *types.ToolRequestUserInputResponse:
		if dec == nil {
			return map[string]any{"answers": map[string]types.ToolRequestUserInputAnswer{}}
		}
		return map[string]any{"answers": normalizeUserInputAnswers(dec.Answers)}
	default:
		// Treat any unknown decision as decline — safer than accept.
		return map[string]any{"decision": "decline", "reason": "unknown decision type"}
	}
}

func normalizeUserInputAnswers(in map[string]types.ToolRequestUserInputAnswer) map[string]types.ToolRequestUserInputAnswer {
	if in == nil {
		return map[string]types.ToolRequestUserInputAnswer{}
	}
	out := make(map[string]types.ToolRequestUserInputAnswer, len(in))
	for id, answer := range in {
		if answer.Answers == nil {
			answer.Answers = []string{}
		}
		out[id] = answer
	}
	return out
}
