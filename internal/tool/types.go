package tool

import "context"

// Tool is a callable capability unit for the LLM.
// Commands are hardcoded inside Execute; the LLM can only select a tool name and provide parameters (primarily a node identifier).
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any // JSON schema (Anthropic tool input definition)
	Execute(ctx context.Context, input map[string]any) (string, error)
}

// FinalResultFormatter is an optional interface. Tools that implement it return a formatted result
// directly to the user after execution, skipping a second LLM summarization pass.
// Suitable for pure query tools (list_nodes, node_status, etc.).
type FinalResultFormatter interface {
	Tool
	// FormatFinalResult formats the raw Execute output into a user-friendly final string.
	// The returned string is used directly as the conversation reply without going through the LLM.
	FormatFinalResult(rawOutput string) string
}
