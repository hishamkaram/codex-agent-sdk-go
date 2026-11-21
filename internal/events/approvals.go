package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
		r.RawParams = bytes.Clone(params)
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

// EncodeApprovalDecision selects the response contract from the incoming
// request. An invalid decision returns a method-specific denial and a
// diagnostic safe to log without exposing request or response content.
func EncodeApprovalDecision(req types.ApprovalRequest, d types.ApprovalDecision) (any, error) {
	switch r := req.(type) {
	case *types.PermissionsApprovalRequest:
		return encodePermissionsDecision(d)
	case *types.ElicitationRequest:
		return encodeElicitationDecision(r, d)
	case *types.ToolRequestUserInputRequest:
		switch dec := d.(type) {
		case types.ToolRequestUserInputResponse:
			return types.ToolRequestUserInputResponse{Answers: normalizeUserInputAnswers(dec.Answers)}, nil
		case *types.ToolRequestUserInputResponse:
			if dec != nil {
				return types.ToolRequestUserInputResponse{Answers: normalizeUserInputAnswers(dec.Answers)}, nil
			}
		}
		return types.ToolRequestUserInputResponse{Answers: normalizeUserInputAnswers(nil)},
			fmt.Errorf("user-input request requires ToolRequestUserInputResponse, got %T", d)
	}
	return encodeGenericApprovalDecision(d), nil
}

func encodeGenericApprovalDecision(d types.ApprovalDecision) map[string]any {
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
	default:
		// Treat any unknown decision as decline — safer than accept.
		return map[string]any{"decision": "decline", "reason": "unknown decision type"}
	}
}

func encodePermissionsDecision(d types.ApprovalDecision) (types.PermissionsApprovalResponse, error) {
	denial := types.PermissionsApprovalResponse{Scope: types.PermissionGrantTurn}
	var grant types.PermissionsApprovalResponse
	switch dec := d.(type) {
	case types.PermissionsApprovalResponse:
		grant = dec
	case *types.PermissionsApprovalResponse:
		if dec == nil {
			return denial, errors.New("permission response is nil")
		}
		grant = *dec
	case nil, types.ApprovalDeny, types.ApprovalCancel:
		return denial, nil
	default:
		return denial, fmt.Errorf("permission grant requires PermissionsApprovalResponse, got %T", d)
	}
	if grant.Scope != types.PermissionGrantTurn && grant.Scope != types.PermissionGrantSession {
		return denial, errors.New("permission grant requires explicit turn or session scope")
	}
	if grant.Scope == types.PermissionGrantSession && grant.StrictAutoReview != nil && *grant.StrictAutoReview {
		return denial, errors.New("strict auto review requires a turn-scoped permission grant")
	}
	if fs := grant.Permissions.FileSystem; fs != nil {
		if err := validateFileSystemGrant(fs); err != nil {
			return denial, err
		}
	}
	return grant, nil
}

func validateFileSystemGrant(fs *types.GrantedFileSystemPermissions) error {
	if fs.GlobScanMaxDepth != nil && *fs.GlobScanMaxDepth == 0 {
		return errors.New("filesystem grant glob scan depth must be positive")
	}
	for _, roots := range [][]string{fs.Read, fs.Write} {
		for _, path := range roots {
			if !isAbsolutePermissionPath(path) {
				return errors.New("filesystem grant roots must be absolute paths")
			}
		}
	}
	for _, entry := range fs.Entries {
		switch entry.Access {
		case types.FileSystemPermissionRead, types.FileSystemPermissionWrite, types.FileSystemPermissionDeny:
		default:
			return errors.New("filesystem grant entry requires read, write, or deny access")
		}
		if !validPermissionPath(entry) {
			return errors.New("filesystem grant entry requires a valid path selection")
		}
	}
	return nil
}

func validPermissionPath(entry types.FileSystemPermissionEntry) bool {
	path := entry.Path
	switch path.Type {
	case types.FileSystemPathLiteral:
		return isAbsolutePermissionPath(path.Path) && path.Pattern == "" && path.Value == nil
	case types.FileSystemPathGlob:
		return entry.Access == types.FileSystemPermissionDeny && validPermissionPathString(path.Pattern) &&
			path.Path == "" && path.Value == nil
	case types.FileSystemPathSpecial:
		return path.Value != nil && path.Path == "" && path.Pattern == "" && validSpecialPermissionPath(path.Value)
	default:
		return false
	}
}

func validSpecialPermissionPath(root *types.FileSystemPermissionSpecial) bool {
	switch root.Kind {
	case "root", "minimal", "tmpdir", "slash_tmp":
		return root.Path == "" && root.Subpath == ""
	case "project_roots":
		return root.Path == "" && !strings.ContainsRune(root.Subpath, 0)
	case "unknown":
		return validPermissionPathString(root.Path) && !strings.ContainsRune(root.Subpath, 0)
	default:
		return false
	}
}

func validPermissionPathString(path string) bool {
	return path != "" && !strings.ContainsRune(path, 0)
}

func isAbsolutePermissionPath(path string) bool {
	if !validPermissionPathString(path) {
		return false
	}
	if path[0] == '/' {
		return true
	}
	// The permission can target a different platform from the SDK process.
	// Preserve both POSIX roots and Windows drive/namespace/UNC spelling.
	if len(path) < 3 {
		return false
	}
	if path[0] == '\\' && windowsPathSeparator(path[1]) {
		return true
	}
	drive := path[0]
	return ((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) &&
		path[1] == ':' && windowsPathSeparator(path[2])
}

func windowsPathSeparator(b byte) bool {
	return b == '\\' || b == '/'
}

func encodeElicitationDecision(req *types.ElicitationRequest, d types.ApprovalDecision) (types.ElicitationResponse, error) {
	denial := types.ElicitationResponse{Action: types.ElicitationDecline}
	var response types.ElicitationResponse
	switch dec := d.(type) {
	case types.ElicitationResponse:
		response = dec
	case *types.ElicitationResponse:
		if dec == nil {
			return denial, errors.New("elicitation response is nil")
		}
		response = *dec
	case types.ApprovalCancel:
		return types.ElicitationResponse{Action: types.ElicitationCancel}, nil
	case nil, types.ApprovalDeny:
		return denial, nil
	default:
		return denial, fmt.Errorf("elicitation requires ElicitationResponse, got %T", d)
	}
	switch response.Action {
	case types.ElicitationAccept:
		if req.Mode != types.ElicitationForm && req.Mode != types.ElicitationURL {
			return denial, errors.New("elicitation accept requires a supported form or URL mode")
		}
	case types.ElicitationDecline, types.ElicitationCancel:
		response.Content = nil
	default:
		return denial, errors.New("elicitation action must be accept, decline, or cancel")
	}
	if response.Content != nil && !json.Valid(response.Content) {
		return denial, errors.New("elicitation content must be valid JSON")
	}
	if response.Meta != nil && !json.Valid(response.Meta) {
		return denial, errors.New("elicitation metadata must be valid JSON")
	}
	return response, nil
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
