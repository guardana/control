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

// TestNoAnswerRelaysAnInputRequest: whatever reaches the agent carries no
// input request and no request state, the members its client would answer
// the upstream from and send the call again with.
func TestNoAnswerRelaysAnInputRequest(t *testing.T) {
	asks := mcp.InputRequestMap{"r1": &mcp.ListRootsParams{}} //nolint:staticcheck // deprecated, yet an upstream can still ask for roots
	call, _ := upstreamResult(&mcp.CallToolResult{InputRequests: asks, RequestState: "s"}).(*mcp.CallToolResult)
	read, _ := upstreamResult(&mcp.ReadResourceResult{InputRequests: asks, RequestState: "s"}).(*mcp.ReadResourceResult)
	prompt, _ := upstreamResult(&mcp.GetPromptResult{InputRequests: asks, RequestState: "s"}).(*mcp.GetPromptResult)
	for name, got := range map[string]struct {
		requests mcp.InputRequestMap
		state    string
	}{
		"a call":   {call.InputRequests, call.RequestState},
		"a read":   {read.InputRequests, read.RequestState},
		"a prompt": {prompt.InputRequests, prompt.RequestState},
	} {
		if got.requests != nil || got.state != "" {
			t.Errorf("%s reaches the agent with input requests %v and request state %q", name, got.requests, got.state)
		}
	}
}
