package types

// ReviewTarget is a tagged union describing what the reviewer should
// look at. Verified against codex 0.121.0 schema — exactly one of
// the four constructors must populate Type.
type ReviewTarget struct {
	// Type is one of: "uncommittedChanges", "baseBranch", "commit",
	// "custom". Use the helper constructors below to build correctly.
	Type string `json:"type"`
	// Branch is required when Type=="baseBranch".
	Branch string `json:"branch,omitempty"`
	// Sha is required when Type=="commit".
	Sha string `json:"sha,omitempty"`
	// Title is optional metadata for Type=="commit" — a UI label
	// (e.g., commit subject).
	Title string `json:"title,omitempty"`
	// Instructions is required when Type=="custom" — the free-form
	// reviewer prompt.
	Instructions string `json:"instructions,omitempty"`
}

// ReviewTargetUncommittedChanges builds a ReviewTarget that reviews
// the working tree (staged + unstaged + untracked).
func ReviewTargetUncommittedChanges() ReviewTarget {
	return ReviewTarget{Type: "uncommittedChanges"}
}

// ReviewTargetBaseBranch builds a ReviewTarget that diffs the current
// branch against `branch`.
func ReviewTargetBaseBranch(branch string) ReviewTarget {
	return ReviewTarget{Type: "baseBranch", Branch: branch}
}

// ReviewTargetCommit builds a ReviewTarget that reviews `sha`. Title
// is an optional UI label.
func ReviewTargetCommit(sha, title string) ReviewTarget {
	return ReviewTarget{Type: "commit", Sha: sha, Title: title}
}

// ReviewTargetCustom builds a ReviewTarget with arbitrary
// instructions — equivalent to the legacy free-form review prompt.
func ReviewTargetCustom(instructions string) ReviewTarget {
	return ReviewTarget{Type: "custom", Instructions: instructions}
}

// ReviewDelivery controls where review results are emitted.
// Verified against codex 0.121.0 schema.
type ReviewDelivery string

const (
	// ReviewInline runs the review on the current thread; review
	// notifications interleave with the thread's existing events.
	// Default.
	ReviewInline ReviewDelivery = "inline"
	// ReviewDetached runs the review on a NEW thread; the new thread
	// id is returned in the RPC response so the caller can subscribe
	// separately.
	ReviewDetached ReviewDelivery = "detached"
)

// ReviewOptions configures `Thread.StartReview`. Mirrors the TUI
// `/review` slash command.
type ReviewOptions struct {
	// Target is REQUIRED — describes what the reviewer evaluates.
	// Use one of the ReviewTarget* helper constructors.
	Target ReviewTarget
	// Delivery is optional ("inline" by default). When "detached",
	// the result's ReviewThreadID is a NEW thread id — the caller
	// should ResumeThread on it to observe the streamed review
	// events.
	Delivery ReviewDelivery
}

// ReviewStartParams is the wire-level request for `review/start`.
// Built from ReviewOptions inside the SDK.
type ReviewStartParams struct {
	ThreadID string         `json:"threadId"`
	Target   ReviewTarget   `json:"target"`
	Delivery ReviewDelivery `json:"delivery,omitempty"`
}

// ReviewStartResult is the sync response of `review/start`. The
// actual review items arrive later as `item/*` notifications on the
// review thread — inline reviews stream into the original thread's
// event channel; detached reviews stream into a new thread's channel
// (the caller must ResumeThread on ReviewThreadID to observe them).
type ReviewStartResult struct {
	// ReviewThreadID is where the review's events will stream. For
	// inline reviews this equals the original thread id; for detached
	// reviews it's a newly-created thread id.
	ReviewThreadID string `json:"reviewThreadId"`
	// Turn is the review's turn descriptor. Items field is always
	// empty in this response — subscribe to notifications for the
	// actual review output.
	Turn ReviewTurn `json:"turn"`
}

// ReviewTurn mirrors the codex wire `Turn` shape returned inside
// ReviewStartResult. The full ThreadItem list is NOT populated here
// (see codex schema comment: "Only populated on thread/resume or
// thread/fork response"); subscribe to notifications for streaming
// review events.
type ReviewTurn struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	StartedAt   *int64 `json:"startedAt,omitempty"`
	CompletedAt *int64 `json:"completedAt,omitempty"`
	DurationMs  *int64 `json:"durationMs,omitempty"`
}
