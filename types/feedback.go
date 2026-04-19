package types

// FeedbackReport is the payload sent to `feedback/upload`. The TUI
// `/feedback` slash command builds an instance of this from user
// input. Verified against codex 0.121.0 schema.
//
// Privacy: IncludeLogs is required by the wire schema (no default).
// When true, codex bundles recent thread logs (user prompts +
// assistant responses) into the upload. The SDK logs a WARN-level
// entry when this is set so callers know the implication.
type FeedbackReport struct {
	// Classification is the report category. REQUIRED. The TUI uses
	// values like "bug" and "feedback" — pass through verbatim.
	Classification string `json:"classification"`
	// IncludeLogs is REQUIRED — pass false to opt out of log upload.
	IncludeLogs bool `json:"includeLogs"`
	// Reason is the free-text body. Optional in the wire schema but
	// recommended for any non-trivial report.
	Reason string `json:"reason,omitempty"`
	// ExtraLogFiles is an optional list of absolute paths whose
	// contents codex will bundle alongside transcript logs.
	ExtraLogFiles []string `json:"extraLogFiles,omitempty"`
}

// FeedbackReceipt is the response of `feedback/upload`. Verified
// against codex 0.121.0 schema — codex returns ONLY a thread ID
// scoping the report.
type FeedbackReceipt struct {
	// ThreadID is the thread the feedback was attached to.
	ThreadID string `json:"threadId"`
}
