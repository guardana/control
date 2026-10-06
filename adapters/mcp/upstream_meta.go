package mcp

import (
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upstreamResult is an upstream answer as the agent sees it: without the
// _meta keys under the gateway's namespace, which only the gateway speaks
// for, on the result and on every block and resource it carries, and without
// input requests or a request state, which would have the agent's client
// answer the upstream and send the call again. It runs after the answer was
// encoded for the record, so the hash stays the upstream's.
func upstreamResult(res mcp.Result) mcp.Result {
	switch r := res.(type) {
	case *mcp.CallToolResult:
		r.Meta = stripMeta(r.Meta)
		r.InputRequests, r.RequestState = nil, ""
		stripContents(r.Content)
	case *mcp.ReadResourceResult:
		r.Meta = stripMeta(r.Meta)
		r.InputRequests, r.RequestState = nil, ""
		for _, c := range r.Contents {
			stripResource(c)
		}
	case *mcp.GetPromptResult:
		r.Meta = stripMeta(r.Meta)
		r.InputRequests, r.RequestState = nil, ""
		for _, m := range r.Messages {
			if m != nil {
				stripContent(m.Content)
			}
		}
	}
	return res
}

func stripContents(blocks []mcp.Content) {
	for _, b := range blocks {
		stripContent(b)
	}
}

// stripContent strips a block's _meta and that of what it nests: an
// embedded resource's contents, a tool result's blocks.
func stripContent(c mcp.Content) {
	if m := contentMeta(c); m != nil {
		*m = stripMeta(*m)
	}
	switch b := c.(type) {
	case *mcp.EmbeddedResource:
		if b != nil {
			stripResource(b.Resource)
		}
	case *mcp.ToolResultContent: //nolint:staticcheck // deprecated for sampling, yet the SDK decodes it in any result an upstream sends
		if b != nil {
			stripContents(b.Content)
		}
	}
}

// contentMeta is the _meta of every kind of block the SDK decodes, or nil for
// a block that is a nil pointer behind the interface.
func contentMeta(c mcp.Content) *mcp.Meta {
	if c == nil || reflect.ValueOf(c).IsNil() {
		return nil
	}
	switch b := c.(type) {
	case *mcp.TextContent:
		return &b.Meta
	case *mcp.ImageContent:
		return &b.Meta
	case *mcp.AudioContent:
		return &b.Meta
	case *mcp.ResourceLink:
		return &b.Meta
	case *mcp.EmbeddedResource:
		return &b.Meta
	case *mcp.ToolUseContent: //nolint:staticcheck // deprecated for sampling, yet the SDK decodes it in any result an upstream sends
		return &b.Meta
	case *mcp.ToolResultContent: //nolint:staticcheck // deprecated for sampling, yet the SDK decodes it in any result an upstream sends
		return &b.Meta
	}
	return nil
}

func stripResource(r *mcp.ResourceContents) {
	if r != nil {
		r.Meta = stripMeta(r.Meta)
	}
}

// errInputRequired ends a call whose upstream asked for input. The requests
// are not relayed, since nothing decides what an upstream asks of the agent,
// and the call is not sent again, since the plane admitted one.
const errInputRequired Error = "the upstream asked for input this gateway does not relay"

// complete is an upstream's answer to a call, a read or a prompt, with an
// answer that asks for input turned into errInputRequired.
func complete(res mcp.Result, err error) (mcp.Result, error) {
	if err == nil && needsInput(res) {
		return nil, errInputRequired
	}
	return res, err
}

// needsInput reports whether res is a call's, a read's or a prompt's answer
// that asks for input. An agent's client acts on input requests whatever
// resultType says, and a request state is only ever echoed in a retry, so
// either one asks, as input_required does.
func needsInput(res mcp.Result) bool {
	switch r := res.(type) {
	case *mcp.CallToolResult:
		return r != nil && asks(r.InputRequests, r.RequestState, r.NeedsInput())
	case *mcp.ReadResourceResult:
		return r != nil && asks(r.InputRequests, r.RequestState, r.NeedsInput())
	case *mcp.GetPromptResult:
		return r != nil && asks(r.InputRequests, r.RequestState, r.NeedsInput())
	}
	return false
}

func asks(requests mcp.InputRequestMap, state string, marked bool) bool {
	return requests != nil || state != "" || marked
}
