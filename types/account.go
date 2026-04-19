package types

import "encoding/json"

// AccountReadResult is the response of `account/read`. Wraps the
// principal Account info and the requiresOpenaiAuth flag.
//
// Wire shape (verified live against codex 0.121.0):
//
//	{"account": {...}, "requiresOpenaiAuth": true}
type AccountReadResult struct {
	Account            Account `json:"account"`
	RequiresOpenaiAuth bool    `json:"requiresOpenaiAuth"`
}

// Account is the authenticated principal codex knows about.
type Account struct {
	// Type is the auth mode codex is using. Observed values:
	// "chatgpt" (ChatGPT subscription), "apikey" (OPENAI_API_KEY env).
	Type string `json:"type,omitempty"`
	// Email is the signed-in user's email when Type="chatgpt". Empty
	// for apikey auth.
	Email string `json:"email,omitempty"`
	// PlanType is the ChatGPT plan tier. Observed: "plus", "pro",
	// "team", "enterprise". Empty for apikey auth.
	PlanType string `json:"planType,omitempty"`
}

// AuthStatus is the response of `getAuthStatus`. Mirrors what the TUI's
// `/status` slash command displays at the top of its output.
//
// Wire shape (verified live):
//
//	{"authMethod": "chatgpt", "authToken": "<jwt>", "requiresOpenaiAuth": true}
//
// SECURITY: AuthToken is a live JWT or API key. Do not log or transmit
// it. Callers should treat AuthStatus as a local-process secret and
// never persist it.
type AuthStatus struct {
	AuthMethod         string `json:"authMethod,omitempty"`
	AuthToken          string `json:"authToken,omitempty"`
	RequiresOpenaiAuth bool   `json:"requiresOpenaiAuth"`
}

// RateLimitsReadResult is the response of `account/rateLimits/read`.
// codex returns BOTH a legacy single-bucket view and a new multi-bucket
// view keyed by limit ID. Callers should prefer RateLimitsByLimitID
// when present and fall back to RateLimits.
//
// Wire shape (verified live):
//
//	{"rateLimits": {...}, "rateLimitsByLimitId": {"codex": {...}, ...}}
type RateLimitsReadResult struct {
	RateLimits          *RateLimits            `json:"rateLimits,omitempty"`
	RateLimitsByLimitID map[string]*RateLimits `json:"rateLimitsByLimitId,omitempty"`
}

// RateLimits is one bucket's snapshot.
type RateLimits struct {
	LimitID   string           `json:"limitId,omitempty"`
	LimitName *string          `json:"limitName,omitempty"`
	PlanType  string           `json:"planType,omitempty"`
	Credits   *Credits         `json:"credits,omitempty"`
	Primary   *RateLimitWindow `json:"primary,omitempty"`
	Secondary *RateLimitWindow `json:"secondary,omitempty"`
	// Raw keeps the full server payload for forward-compat fields the
	// SDK has not yet typed.
	Raw json.RawMessage `json:"-"`
}

// Credits is a credit-balance snapshot. Balance is a string because
// codex serializes it as a stringified integer (preserves precision for
// large balances).
type Credits struct {
	Balance    string `json:"balance,omitempty"`
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
}

// RateLimitWindow describes one time-window bucket (primary = short
// window, secondary = long window).
type RateLimitWindow struct {
	// ResetsAt is the Unix epoch seconds when this window resets to 0.
	ResetsAt int64 `json:"resetsAt,omitempty"`
	// UsedPercent is 0..100, the fraction of the window already
	// consumed. Codex returns this as an integer, NOT a float.
	UsedPercent int `json:"usedPercent"`
	// WindowDurationMins is the rolling-window length in minutes.
	WindowDurationMins int `json:"windowDurationMins,omitempty"`
}
