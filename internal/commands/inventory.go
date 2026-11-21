package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const maxModelListPages = 100

func ListModels(ctx context.Context, send Send, paginationError error) ([]types.ModelInfo, error) {
	var opts types.ModelListOptions
	models := []types.ModelInfo{}
	seenCursors := make(map[string]struct{})
	for range maxModelListPages {
		page, err := listModelsPage(ctx, send, "ListModels", opts)
		if err != nil {
			return nil, err
		}
		if page.Data == nil {
			// Older providers may omit data on a single terminal page.
			if opts.Cursor == nil && page.NextCursor == nil {
				return nil, nil
			}
			return nil, fmt.Errorf("codex.Client.ListModels: response data must be a non-null array")
		}
		models = append(models, page.Data...)
		if page.NextCursor == nil {
			return models, nil
		}
		cursor := *page.NextCursor
		if _, seen := seenCursors[cursor]; seen {
			return nil, fmt.Errorf("codex.Client.ListModels: repeated cursor: %w", paginationError)
		}
		seenCursors[cursor] = struct{}{}
		opts.Cursor = page.NextCursor
	}
	return nil, fmt.Errorf("codex.Client.ListModels: exceeded %d pages: %w", maxModelListPages, paginationError)
}

func ListModelsPage(ctx context.Context, send Send, opts *types.ModelListOptions) (*types.ModelListResult, error) {
	var params types.ModelListOptions
	if opts != nil {
		params = *opts
	}
	page, err := listModelsPage(ctx, send, "ListModelsPage", params)
	if err != nil {
		return nil, err
	}
	if page.Data == nil {
		return nil, fmt.Errorf("codex.Client.ListModelsPage: response data must be a non-null array")
	}
	return page, nil
}

func listModelsPage(ctx context.Context, send Send, callerName string, params types.ModelListOptions) (*types.ModelListResult, error) {
	resp, err := send(ctx, callerName, "model/list", params)
	if err != nil {
		return nil, err
	}
	var result types.ModelListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.%s: decode response: %w", callerName, err)
	}
	return &result, nil
}

func ListExperimentalFeatures(ctx context.Context, send Send) ([]types.ExperimentalFeature, error) {
	resp, err := send(ctx, "ListExperimentalFeatures", "experimentalFeature/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.ExperimentalFeatureListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListExperimentalFeatures: decode response: %w", err)
	}
	return result.Data, nil
}

func ListMCPServerStatus(ctx context.Context, send Send) (*types.MCPServerStatusListResult, error) {
	return listMCPServerStatusPage(ctx, send, "ListMCPServerStatus", types.MCPServerStatusListOptions{})
}

func ListMCPServerStatusPage(ctx context.Context, send Send, opts *types.MCPServerStatusListOptions) (*types.MCPServerStatusListResult, error) {
	var params types.MCPServerStatusListOptions
	if opts != nil {
		params = *opts
	}
	if params.EnvironmentID != "" && (strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.ServerName) == "") {
		return nil, fmt.Errorf("codex.Client.ListMCPServerStatusPage: environment ID requires a thread ID and server name")
	}
	switch params.Detail {
	case "", types.MCPServerStatusDetailFull, types.MCPServerStatusDetailToolsAndAuthOnly:
	default:
		return nil, fmt.Errorf("codex.Client.ListMCPServerStatusPage: invalid inventory detail")
	}
	page, err := listMCPServerStatusPage(ctx, send, "ListMCPServerStatusPage", params)
	if err != nil {
		return nil, err
	}
	if page.Data == nil {
		return nil, fmt.Errorf("codex.Client.ListMCPServerStatusPage: response data must be a non-null array")
	}
	return page, nil
}

func listMCPServerStatusPage(ctx context.Context, send Send, callerName string, params types.MCPServerStatusListOptions) (*types.MCPServerStatusListResult, error) {
	resp, err := send(ctx, callerName, "mcpServerStatus/list", params)
	if err != nil {
		return nil, err
	}
	var result types.MCPServerStatusListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.%s: decode response: %w", callerName, err)
	}
	return &result, nil
}

func ListApps(ctx context.Context, send Send) ([]types.AppInfo, error) {
	resp, err := send(ctx, "ListApps", "app/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.AppListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListApps: decode response: %w", err)
	}
	return result.Data, nil
}

func ListSkills(ctx context.Context, send Send, defaultCwd string) ([]types.SkillsCwdGroup, error) {
	resp, err := send(ctx, "ListSkills", "skills/list", buildSkillsListParams(defaultCwd))
	if err != nil {
		return nil, err
	}
	var result types.SkillsListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListSkills: decode response: %w", err)
	}
	return result.Data, nil
}

func buildSkillsListParams(defaultCwd string) map[string]any {
	params := map[string]any{}
	if defaultCwd != "" {
		params["cwds"] = []string{defaultCwd}
	}
	return params
}
