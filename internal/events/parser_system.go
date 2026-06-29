package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func wrapRealtime(raw json.RawMessage, build func(threadID string, params json.RawMessage) types.ThreadEvent) (types.ThreadEvent, error) {
	var ids struct {
		ThreadID string `json:"threadId"`
	}
	_ = unmarshalTo(raw, &ids)
	return build(ids.ThreadID, cloneRaw(raw)), nil
}

func parseMCPServerStartupStatus(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Name   string          `json:"name"`
		Status json.RawMessage `json:"status"`
		Error  *string         `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.MCPServerStartupStatusUpdated{
		Name:   env.Name,
		Status: cloneRaw(env.Status),
		Error:  env.Error,
	}, nil
}

func parseMCPServerOAuthLoginCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Name    string  `json:"name"`
		Success bool    `json:"success"`
		Error   *string `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.MCPServerOAuthLoginCompleted{Name: env.Name, Success: env.Success, Error: env.Error}, nil
}

func parseAccountLoginCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Success bool    `json:"success"`
		LoginID *string `json:"loginId"`
		Error   *string `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AccountLoginCompleted{Success: env.Success, LoginID: env.LoginID, Error: env.Error}, nil
}

func parseAccountRateLimitsUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		RateLimits json.RawMessage `json:"rateLimits"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AccountRateLimitsUpdated{RateLimits: cloneRaw(env.RateLimits)}, nil
}

func parseAccountUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		AuthMode json.RawMessage `json:"authMode"`
		PlanType json.RawMessage `json:"planType"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AccountUpdated{AuthMode: cloneRaw(env.AuthMode), PlanType: cloneRaw(env.PlanType)}, nil
}

func parseModelRerouted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		FromModel string          `json:"fromModel"`
		ToModel   string          `json:"toModel"`
		Reason    json.RawMessage `json:"reason"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ModelRerouted{
		ThreadID:  env.ThreadID,
		TurnID:    env.TurnID,
		FromModel: env.FromModel,
		ToModel:   env.ToModel,
		Reason:    cloneRaw(env.Reason),
	}, nil
}

func parseModelVerification(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID      string          `json:"threadId"`
		TurnID        string          `json:"turnId"`
		Verifications json.RawMessage `json:"verifications"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ModelVerification{
		ThreadID:      env.ThreadID,
		TurnID:        env.TurnID,
		Verifications: cloneRaw(env.Verifications),
	}, nil
}

func parseConfigWarning(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Summary string          `json:"summary"`
		Details *string         `json:"details"`
		Path    *string         `json:"path"`
		Range   json.RawMessage `json:"range"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ConfigWarning{Summary: env.Summary, Details: env.Details, Path: env.Path, Range: cloneRaw(env.Range)}, nil
}

func parseWarning(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID *string `json:"threadId"`
		Message  string  `json:"message"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.Warning{ThreadID: env.ThreadID, Message: env.Message}, nil
}

func parseGuardianWarning(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string `json:"threadId"`
		Message  string `json:"message"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.GuardianWarning{ThreadID: env.ThreadID, Message: env.Message}, nil
}

func parseDeprecationNotice(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Summary string  `json:"summary"`
		Details *string `json:"details"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.DeprecationNotice{Summary: env.Summary, Details: env.Details}, nil
}

func parseFsChanged(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		WatchID      string   `json:"watchId"`
		ChangedPaths []string `json:"changedPaths"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.FsChanged{WatchID: env.WatchID, ChangedPaths: env.ChangedPaths}, nil
}

func parseAppListUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AppListUpdated{Data: cloneRaw(env.Data)}, nil
}

func parseServerRequestResolved(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ServerRequestResolved{ThreadID: env.ThreadID, RequestID: cloneRaw(env.RequestID)}, nil
}

func parseProcessOutputDelta(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ProcessHandle string `json:"processHandle"`
		Stream        string `json:"stream"`
		DeltaBase64   string `json:"deltaBase64"`
		CapReached    bool   `json:"capReached"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ProcessOutputDelta{
		ProcessHandle: env.ProcessHandle,
		Stream:        env.Stream,
		DeltaBase64:   env.DeltaBase64,
		CapReached:    env.CapReached,
	}, nil
}

func parseProcessExited(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ProcessHandle    string `json:"processHandle"`
		ExitCode         int    `json:"exitCode"`
		Stdout           string `json:"stdout"`
		Stderr           string `json:"stderr"`
		StdoutCapReached bool   `json:"stdoutCapReached"`
		StderrCapReached bool   `json:"stderrCapReached"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ProcessExited{
		ProcessHandle:    env.ProcessHandle,
		ExitCode:         env.ExitCode,
		Stdout:           env.Stdout,
		Stderr:           env.Stderr,
		StdoutCapReached: env.StdoutCapReached,
		StderrCapReached: env.StderrCapReached,
	}, nil
}

func parseCommandExecOutputDelta(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ProcessID   string `json:"processId"`
		Stream      string `json:"stream"`
		DeltaBase64 string `json:"deltaBase64"`
		CapReached  bool   `json:"capReached"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.CommandExecOutputDelta{
		ProcessID:   env.ProcessID,
		Stream:      env.Stream,
		DeltaBase64: env.DeltaBase64,
		CapReached:  env.CapReached,
	}, nil
}

func parseRemoteControlStatusChanged(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		EnvironmentID *string `json:"environmentId"`
		Status        string  `json:"status"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.RemoteControlStatusChanged{
		EnvironmentID: env.EnvironmentID,
		Status:        env.Status,
	}, nil
}

func parseWindowsWorldWritableWarning(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ExtraCount  int      `json:"extraCount"`
		FailedScan  bool     `json:"failedScan"`
		SamplePaths []string `json:"samplePaths"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.WindowsWorldWritableWarning{
		ExtraCount:  env.ExtraCount,
		FailedScan:  env.FailedScan,
		SamplePaths: env.SamplePaths,
	}, nil
}

// parseHookEvent handles both hook/started and hook/completed. started is
// true for hook/started, false for hook/completed.
func parseHookEvent(raw json.RawMessage, started bool) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string               `json:"threadId"`
		TurnID   *string              `json:"turnId"`
		Run      types.HookRunSummary `json:"run"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	if started {
		return &types.HookStarted{ThreadID: env.ThreadID, TurnID: env.TurnID, Run: env.Run}, nil
	}
	return &types.HookCompleted{ThreadID: env.ThreadID, TurnID: env.TurnID, Run: env.Run}, nil
}

func parseWindowsSandboxSetupCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Mode    json.RawMessage `json:"mode"`
		Success bool            `json:"success"`
		Error   *string         `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.WindowsSandboxSetupCompleted{
		Success: env.Success,
		Mode:    cloneRaw(env.Mode),
		Error:   env.Error,
	}, nil
}
