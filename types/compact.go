package types

// CompactOptions configures `Thread.Compact`. The zero value is valid
// and triggers codex's default compaction strategy.
//
// Compact is hybrid sync/async: the RPC ack returns immediately, and
// the matching `*ContextCompacted` notification arrives later on the
// thread's event channel. The SDK exposes a `*CompactResult.Wait(ctx)`
// helper so callers can choose to block.
type CompactOptions struct {
	// Strategy is reserved for future codex versions that may expose
	// alternate compaction algorithms. Empty string = server default.
	// Observed values from codex binary strings: none yet (server
	// always uses its built-in summarizer in 0.121.0).
	Strategy string `json:"strategy,omitempty"`
}

// CompactStartParams is the request shape for `thread/compact/start`.
// Verified live (returns empty `{}` ack):
//
//	{"threadId": "..."}
//
// Strategy is included for forward-compat; codex 0.121.0 ignores it.
type CompactStartParams struct {
	ThreadID string `json:"threadId"`
	Strategy string `json:"strategy,omitempty"`
}
