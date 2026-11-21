package threadhistory

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func parseThreadInfo(raw json.RawMessage) (types.ThreadInfo, error) {
	var row struct {
		ID            string          `json:"id"`
		SessionID     string          `json:"sessionId"`
		Name          string          `json:"name"`
		Preview       string          `json:"preview"`
		Cwd           string          `json:"cwd"`
		Model         string          `json:"model"`
		ModelProvider json.RawMessage `json:"modelProvider"`
		Path          string          `json:"path"`
		UpdatedAt     string          `json:"updatedAt"`
		CreatedAt     string          `json:"createdAt"`
		Archived      bool            `json:"archived"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return types.ThreadInfo{}, types.NewJSONDecodeError(string(raw), err)
	}
	id := row.ID
	if id == "" {
		id = row.SessionID
	}
	model := row.Model
	if model == "" && len(row.ModelProvider) > 0 {
		model = string(row.ModelProvider)
	}
	lastModified := row.UpdatedAt
	if lastModified == "" {
		lastModified = row.CreatedAt
	}
	if lastModified == "" {
		lastModified = row.Path
	}
	summary := row.Preview
	if summary == "" {
		summary = row.Name
	}
	return types.ThreadInfo{
		ThreadID:     id,
		Summary:      summary,
		LastModified: lastModified,
		Cwd:          row.Cwd,
		Model:        model,
		Archived:     row.Archived,
		Raw:          cloneRaw(raw),
	}, nil
}

func parseThreadRecord(raw json.RawMessage) (types.ThreadRecord, error) {
	var row struct {
		ID            string            `json:"id"`
		SessionID     string            `json:"sessionId"`
		Name          string            `json:"name"`
		Preview       string            `json:"preview"`
		Cwd           string            `json:"cwd"`
		Model         string            `json:"model"`
		ModelProvider json.RawMessage   `json:"modelProvider"`
		Path          string            `json:"path"`
		Status        string            `json:"status"`
		CreatedAt     string            `json:"createdAt"`
		UpdatedAt     string            `json:"updatedAt"`
		Turns         []json.RawMessage `json:"turns"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return types.ThreadRecord{}, types.NewJSONDecodeError(string(raw), err)
	}
	id := row.ID
	if id == "" {
		id = row.SessionID
	}
	model := row.Model
	if model == "" && len(row.ModelProvider) > 0 {
		model = string(row.ModelProvider)
	}
	turns := make([]types.ThreadHistoryTurn, 0, len(row.Turns))
	for _, turnRaw := range row.Turns {
		turn, err := parseThreadHistoryTurn(turnRaw)
		if err != nil {
			return types.ThreadRecord{}, err
		}
		turns = append(turns, turn)
	}
	return types.ThreadRecord{
		ID:        id,
		SessionID: row.SessionID,
		Name:      row.Name,
		Preview:   row.Preview,
		Cwd:       row.Cwd,
		Model:     model,
		Path:      row.Path,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Turns:     turns,
		Raw:       cloneRaw(raw),
	}, nil
}

func parseThreadHistoryTurn(raw json.RawMessage) (types.ThreadHistoryTurn, error) {
	var row struct {
		ID          string            `json:"id"`
		Status      string            `json:"status"`
		StartedAt   string            `json:"startedAt"`
		CompletedAt string            `json:"completedAt"`
		Items       []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return types.ThreadHistoryTurn{}, types.NewJSONDecodeError(string(raw), err)
	}
	items := make([]types.ThreadHistoryItem, 0, len(row.Items))
	for _, itemRaw := range row.Items {
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(itemRaw, &probe); err != nil {
			return types.ThreadHistoryTurn{}, types.NewJSONDecodeError(string(itemRaw), err)
		}
		items = append(items, types.ThreadHistoryItem{
			Type: probe.Type,
			ID:   probe.ID,
			Raw:  cloneRaw(itemRaw),
		})
	}
	return types.ThreadHistoryTurn{
		ID:          row.ID,
		Status:      row.Status,
		StartedAt:   row.StartedAt,
		CompletedAt: row.CompletedAt,
		Items:       items,
		Raw:         cloneRaw(raw),
	}, nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp
}
