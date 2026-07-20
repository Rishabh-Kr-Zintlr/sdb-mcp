package tools

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NoArgs is the input type for tools that take no arguments. It yields an empty
// object input schema.
type NoArgs struct{}

// JSONResult marshals v to compact JSON and returns it as the text content of a
// successful tool result. It is the standard success path for the read-only
// tools, preserving upstream Extended JSON verbatim (json.RawMessage fields
// marshal through unchanged).
func JSONResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return TextResult(string(b)), nil, nil
}

// TextResult wraps a string as a single-block successful tool result.
func TextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
