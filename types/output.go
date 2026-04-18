package types

// OutputSchema constrains the model's final response to match a JSON Schema.
// Passed per-turn via RunOptions.OutputSchema — not per-thread.
//
// Schema is the raw JSON Schema (typically a map[string]any). Name and
// Description are optional but recommended — the model uses them to
// understand the schema's intent.
type OutputSchema struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema"`
	Strict      bool           `json:"strict,omitempty"`
}
