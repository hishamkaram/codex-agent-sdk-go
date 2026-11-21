package types

// PrepareApprovedPlanOptions supplies optional settings for preparing the
// first turn of a fresh implementation thread.
type PrepareApprovedPlanOptions struct {
	// AttachmentPath is an absolute path on the app-server host. Codex
	// decides whether the destination can read it and whether to use it.
	AttachmentPath string `json:"attachmentPath,omitempty"`
}

// PreparedApprovedPlan is the provider's preparation result. Preparation
// may already have added the approved plan to the destination history.
type PreparedApprovedPlan struct {
	// Prompt is the exact text to submit once as the destination's first turn.
	Prompt string `json:"prompt"`
}
