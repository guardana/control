package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestATypedNilBlockIsNotACrash: a result holding a block that is a nil
// pointer behind the Content interface, at the top or nested, is passed on,
// not dereferenced.
func TestATypedNilBlockIsNotACrash(_ *testing.T) {
	results := []mcp.Result{
		&mcp.CallToolResult{Content: []mcp.Content{(*mcp.TextContent)(nil), (*mcp.ImageContent)(nil), (*mcp.AudioContent)(nil),
			(*mcp.ResourceLink)(nil), (*mcp.EmbeddedResource)(nil), (*mcp.ToolUseContent)(nil), (*mcp.ToolResultContent)(nil), //nolint:staticcheck // the SDK decodes them in any result
			&mcp.ToolResultContent{Content: []mcp.Content{(*mcp.TextContent)(nil)}}}}, //nolint:staticcheck // the SDK decodes it in any result
		&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{nil}},
		&mcp.GetPromptResult{Messages: []*mcp.PromptMessage{nil, {Content: (*mcp.TextContent)(nil)}}},
	}
	for _, res := range results {
		upstreamResult(res)
	}
}
