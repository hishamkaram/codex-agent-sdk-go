package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func ReadAccount(ctx context.Context, send Send) (*types.AccountReadResult, error) {
	resp, err := send(ctx, "ReadAccount", "account/read", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.AccountReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadAccount: decode response: %w", err)
	}
	return &result, nil
}

func ReadRateLimits(ctx context.Context, send Send) (*types.RateLimitsReadResult, error) {
	resp, err := send(ctx, "ReadRateLimits", "account/rateLimits/read", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.RateLimitsReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadRateLimits: decode response: %w", err)
	}
	return &result, nil
}

func GetAuthStatus(ctx context.Context, send Send) (*types.AuthStatus, error) {
	resp, err := send(ctx, "GetAuthStatus", "getAuthStatus", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.AuthStatus
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.GetAuthStatus: decode response: %w", err)
	}
	return &result, nil
}

func Logout(ctx context.Context, send Send) error {
	_, err := send(ctx, "Logout", "account/logout", map[string]any{})
	return err
}

func UploadFeedback(ctx context.Context, send Send, report types.FeedbackReport, warn func(classification string)) (*types.FeedbackReceipt, error) {
	if report.Classification == "" {
		return nil, fmt.Errorf("codex.Client.UploadFeedback: report.Classification is required")
	}
	if report.IncludeLogs {
		warn(report.Classification)
	}
	resp, err := send(ctx, "UploadFeedback", "feedback/upload", report)
	if err != nil {
		return nil, err
	}
	var receipt types.FeedbackReceipt
	if err := json.Unmarshal(resp, &receipt); err != nil {
		return nil, fmt.Errorf("codex.Client.UploadFeedback: decode response: %w", err)
	}
	return &receipt, nil
}
