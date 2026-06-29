package types

import "encoding/json"

// --- v0.2.0 expansion: turn-scoped events ---

// TurnDiffUpdated is emitted when the server updates the aggregated diff
// for a turn. Wire method: "turn/diff/updated".
type TurnDiffUpdated struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
	Diff     string `json:"diff"`
}

func (*TurnDiffUpdated) isThreadEvent()      {}
func (*TurnDiffUpdated) EventMethod() string { return "turn/diff/updated" }

// TurnPlanUpdated is emitted when the server updates the plan for a turn.
// Plan is the raw plan payload (array of steps); Explanation is optional
// context the server included.
// Wire method: "turn/plan/updated".
type TurnPlanUpdated struct {
	ThreadID    string          `json:"thread_id"`
	TurnID      string          `json:"turn_id"`
	Plan        json.RawMessage `json:"plan"`
	Explanation *string         `json:"explanation,omitempty"`
}

func (*TurnPlanUpdated) isThreadEvent()      {}
func (*TurnPlanUpdated) EventMethod() string { return "turn/plan/updated" }

// TurnModerationMetadata is emitted when the server attaches moderation
// metadata to a turn. Metadata is preserved raw because the schema accepts any
// JSON payload.
// Wire method: "turn/moderationMetadata".
type TurnModerationMetadata struct {
	ThreadID string          `json:"thread_id"`
	TurnID   string          `json:"turn_id"`
	Metadata json.RawMessage `json:"metadata"`
}

func (*TurnModerationMetadata) isThreadEvent() {}
func (*TurnModerationMetadata) EventMethod() string {
	return "turn/moderationMetadata"
}

// --- v0.2.0 expansion: guardian auto-approval review events ---

// ItemGuardianApprovalReviewStarted is emitted when the server delegates
// an approval decision to an automated guardian subagent.
// Wire method: "item/autoApprovalReview/started".
type ItemGuardianApprovalReviewStarted struct {
	ThreadID     string          `json:"thread_id"`
	TurnID       string          `json:"turn_id"`
	ReviewID     string          `json:"review_id"`
	TargetItemID *string         `json:"target_item_id,omitempty"`
	Action       json.RawMessage `json:"action"`
	Review       json.RawMessage `json:"review"`
}

func (*ItemGuardianApprovalReviewStarted) isThreadEvent() {}
func (*ItemGuardianApprovalReviewStarted) EventMethod() string {
	return "item/autoApprovalReview/started"
}

// ItemGuardianApprovalReviewCompleted is emitted when the guardian
// subagent reaches a decision. DecisionSource indicates whether the
// decision came from policy rules or the subagent LLM.
// Wire method: "item/autoApprovalReview/completed".
type ItemGuardianApprovalReviewCompleted struct {
	ThreadID       string          `json:"thread_id"`
	TurnID         string          `json:"turn_id"`
	ReviewID       string          `json:"review_id"`
	TargetItemID   *string         `json:"target_item_id,omitempty"`
	Action         json.RawMessage `json:"action"`
	Review         json.RawMessage `json:"review"`
	DecisionSource json.RawMessage `json:"decision_source"`
}

func (*ItemGuardianApprovalReviewCompleted) isThreadEvent() {}
func (*ItemGuardianApprovalReviewCompleted) EventMethod() string {
	return "item/autoApprovalReview/completed"
}

// FileChangePatchUpdated carries the latest patch changes for a fileChange
// item. Changes is raw to preserve the upstream PatchChangeKind union.
// Wire method: "item/fileChange/patchUpdated".
type FileChangePatchUpdated struct {
	ThreadID string          `json:"thread_id"`
	TurnID   string          `json:"turn_id"`
	ItemID   string          `json:"item_id"`
	Changes  json.RawMessage `json:"changes"`
}

func (*FileChangePatchUpdated) isThreadEvent() {}
func (*FileChangePatchUpdated) EventMethod() string {
	return "item/fileChange/patchUpdated"
}
