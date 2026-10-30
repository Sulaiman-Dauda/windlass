package mcp

import "encoding/json"

// Output limits. A tool that hits one says so in its result rather than
// silently cutting the list.
const (
	MaxListItems = 200
	MaxLogLines  = 500
	MaxLogBytes  = 64 << 10
)

// Tool is one entry in the catalogue, as sent in tools/list.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  Annotations     `json:"annotations"`
}

// Annotations are hints clients use to decide when to ask the user. Every
// tool sets all four explicitly, because the spec defaults assume the worst
// (not read-only, destructive) and Claude's connector review requires them.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// Tools returns the catalogue in a stable order. Unit 1 has the read-only
// tools: list_projects, get_project, list_deployments, get_deployment,
// get_logs, get_staging, list_backups. No tool returns env values or files
// under a project's env path, and none exists for routes that refuse tokens.
func Tools() []Tool {
	panic("scaffold: return the tool catalogue")
}
