package threadhistory

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func parseThreadInfo(raw json.RawMessage) (types.ThreadInfo, error) {
	var row struct {
		ID            string           `json:"id"`
		SessionID     string           `json:"sessionId"`
		Name          string           `json:"name"`
		Preview       string           `json:"preview"`
		Cwd           string           `json:"cwd"`
		Model         string           `json:"model"`
		ModelProvider json.RawMessage  `json:"modelProvider"`
		Path          string           `json:"path"`
		UpdatedAt     historyTimestamp `json:"updatedAt"`
		CreatedAt     historyTimestamp `json:"createdAt"`
		Archived      bool             `json:"archived"`
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
	lastModified := string(row.UpdatedAt)
	if lastModified == "" {
		lastModified = string(row.CreatedAt)
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
		ID            string              `json:"id"`
		SessionID     string              `json:"sessionId"`
		Name          string              `json:"name"`
		Preview       string              `json:"preview"`
		Cwd           string              `json:"cwd"`
		Model         string              `json:"model"`
		ModelProvider json.RawMessage     `json:"modelProvider"`
		Path          string              `json:"path"`
		Status        historyThreadStatus `json:"status"`
		CreatedAt     historyTimestamp    `json:"createdAt"`
		UpdatedAt     historyTimestamp    `json:"updatedAt"`
		Turns         []json.RawMessage   `json:"turns"`
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
		Status:    string(row.Status),
		CreatedAt: string(row.CreatedAt),
		UpdatedAt: string(row.UpdatedAt),
		Turns:     turns,
		Raw:       cloneRaw(raw),
	}, nil
}

func parseThreadHistoryTurn(raw json.RawMessage) (types.ThreadHistoryTurn, error) {
	var row struct {
		ID          string            `json:"id"`
		Status      string            `json:"status"`
		StartedAt   historyTimestamp  `json:"startedAt"`
		CompletedAt historyTimestamp  `json:"completedAt"`
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
		StartedAt:   string(row.StartedAt),
		CompletedAt: string(row.CompletedAt),
		Items:       items,
		Raw:         cloneRaw(raw),
	}, nil
}

// historyTimestamp exposes provider Unix seconds as UTC RFC3339 while
// preserving timestamps supplied as strings by existing peers.
type historyTimestamp string

func (stamp *historyTimestamp) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && (data[0] == '"' || data[0] == 'n') {
		return json.Unmarshal(data, (*string)(stamp))
	}
	var seconds int64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return err
	}
	text, err := time.Unix(seconds, 0).UTC().MarshalText()
	if err != nil {
		return err
	}
	*stamp = historyTimestamp(text)
	return nil
}

type historyThreadStatus string

func (status *historyThreadStatus) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] != '{' {
		return json.Unmarshal(data, (*string)(status))
	}
	var tagged struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return err
	}
	if tagged.Type == "" {
		return errors.New("thread status object has no type")
	}
	*status = historyThreadStatus(tagged.Type)
	return nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp
}
