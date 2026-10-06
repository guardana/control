package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// messageInputRequired is the fixed answer to a call whose upstream asked
// for input, pinned as the agent reads it.
const messageInputRequired = "the upstream asked for input this gateway does not relay"

// asking is what the victim's read_file, resource and prompt answer: input
// requests, with no content, or content with a request state beside it.
type asking struct {
	requests func() sdk.InputRequestMap
	state    string
}

// askingVictim is a victim whose read_file, resource and prompt answer as
// ask says, and which counts every call, read and prompt that reaches it.
func askingVictim(ask asking) (*victim, func(string) int) {
	v := newVictim()
	var mu sync.Mutex
	seen := map[string]int{}
	v.server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			mu.Lock()
			seen[m]++
			mu.Unlock()
			return next(ctx, m, req)
		}
	})
	requests := func() sdk.InputRequestMap {
		if ask.requests == nil {
			return nil
		}
		return ask.requests()
	}
	v.replaced["read_file"] = func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		out := &sdk.CallToolResult{InputRequests: requests(), RequestState: ask.state}
		if ask.requests == nil {
			out.Content = []sdk.Content{&sdk.TextContent{Text: upstreamMark}}
		}
		return out, nil
	}
	v.server.AddResource(&sdk.Resource{URI: "file:///r", Name: "r"}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		out := &sdk.ReadResourceResult{InputRequests: requests(), RequestState: ask.state}
		if ask.requests == nil {
			out.Contents = []*sdk.ResourceContents{{URI: "file:///r", Text: upstreamMark}}
		}
		return out, nil
	})
	v.server.AddPrompt(&sdk.Prompt{Name: "p"}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		out := &sdk.GetPromptResult{InputRequests: requests(), RequestState: ask.state}
		if ask.requests == nil {
			out.Messages = []*sdk.PromptMessage{{Role: "user", Content: &sdk.TextContent{Text: upstreamMark}}}
		}
		return out, nil
	})
	return v, func(m string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[m]
	}
}

// rewritingTransport hands the adapter every result its upstream answers
// after rewrite edited the result's members.
type rewritingTransport struct {
	sdk.Transport
	rewrite func(map[string]json.RawMessage)
}

func (r rewritingTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	conn, err := r.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return rewritingConn{Connection: conn, rewrite: r.rewrite}, nil
}

type rewritingConn struct {
	sdk.Connection
	rewrite func(map[string]json.RawMessage)
}

func (c rewritingConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	resp, ok := msg.(*jsonrpc.Response)
	if err != nil || !ok || len(resp.Result) == 0 {
		return msg, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &members); err != nil {
		return nil, err
	}
	c.rewrite(members)
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, err
	}
	resp.Result = raw
	return resp, nil
}

// resultTypes are the ways an upstream can mark an answer that asks for
// input: as its SDK does, with no resultType at all, or calling it complete.
var resultTypes = []struct {
	name    string
	rewrite func(map[string]json.RawMessage)
}{
	{"as the SDK marks it", nil},
	{"no resultType", func(m map[string]json.RawMessage) { delete(m, "resultType") }},
	{"resultType complete", func(m map[string]json.RawMessage) { m["resultType"] = json.RawMessage(`"complete"`) }},
}

// askingMethods are the three methods whose answer can ask for input.
var askingMethods = []struct {
	method string
	do     func(context.Context, *sdk.ClientSession) error
}{
	{"tools/call", func(ctx context.Context, cs *sdk.ClientSession) error {
		_, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "/x"}})
		return err
	}},
	{"resources/read", func(ctx context.Context, cs *sdk.ClientSession) error {
		_, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "file:///r"})
		return err
	}},
	{"prompts/get", func(ctx context.Context, cs *sdk.ClientSession) error {
		_, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "p"})
		return err
	}},
}

// TestInputRequiredIsSentOnceAndFails: an upstream answering a call, a
// read or a prompt with input requests or a request state, for load shedding
// or with a roots request the client could fulfil itself, receives the one
// request the plane admitted, whatever resultType it gives the answer. The
// agent is told the call failed with a fixed answer, nothing of the
// upstream's requests is relayed, and the trail holds one execution that
// ended failed.
func TestInputRequiredIsSentOnceAndFails(t *testing.T) {
	asks := []struct {
		name string
		ask  asking
	}{
		{"empty", asking{requests: func() sdk.InputRequestMap { return sdk.InputRequestMap{} }}},
		{"roots", asking{requests: func() sdk.InputRequestMap { return sdk.InputRequestMap{"r1": &sdk.ListRootsParams{}} }}}, //nolint:staticcheck // deprecated, yet an upstream can still ask for roots
		{"a request state alone", asking{state: "state-of-the-upstream"}},
	}
	for _, a := range asks {
		for _, rt := range resultTypes {
			for _, m := range askingMethods {
				t.Run(a.name+"/"+rt.name+"/"+m.method, func(t *testing.T) {
					wantSentOnceAndFailed(t, a.ask, rt.rewrite, m.method, m.do)
				})
			}
		}
	}
	// An answer marked input_required asks for input even with no request
	// and no state beside the mark.
	markOnly := func(m map[string]json.RawMessage) { m["resultType"] = json.RawMessage(`"input_required"`) }
	for _, m := range askingMethods {
		t.Run("the mark alone/"+m.method, func(t *testing.T) {
			wantSentOnceAndFailed(t, asking{}, markOnly, m.method, m.do)
		})
	}
}

// wantSentOnceAndFailed fails unless an upstream answering as ask says, its
// answer rewritten by rewrite, receives one request for method, and the agent
// is told the call failed with the fixed answer on a trail with one failed
// execution.
func wantSentOnceAndFailed(t *testing.T, ask asking, rewrite func(map[string]json.RawMessage), method string, do func(context.Context, *sdk.ClientSession) error) {
	t.Helper()
	v, seen := askingVictim(ask)
	r := newRigOver(t, v, mcp.KindStdio, rigOptions{mode: modeEnforce, rules: []string{allowReads}, rewrite: rewrite})
	err := do(ctxT(t), r.connect(t, "agent-a"))
	if n := seen(method); n != 1 {
		t.Errorf("the upstream received %d %s request(s) for one admitted call, want 1", n, method)
	}
	var werr *jsonrpc.Error
	if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeInternalError || werr.Message != messageInputRequired {
		t.Errorf("the agent was answered %v, want code %d and %q", err, jsonrpc.CodeInternalError, messageInputRequired)
	}
	wantOneFailedExecution(t, r)
}

// wantOneFailedExecution fails unless the trail started one execution and
// recorded its end as a failure the upstream's input request caused.
func wantOneFailedExecution(t *testing.T, r *rig) {
	t.Helper()
	kinds := r.kindsOf(t, "")
	started := 0
	for _, k := range kinds {
		if k == controlv1.EventKind_EVENT_KIND_ACTION_STARTED {
			started++
		}
	}
	if started != 1 || len(kinds) == 0 || kinds[len(kinds)-1] != kindFailed {
		t.Fatalf("the trail is %v, want one ACTION_STARTED and ACTION_FAILED last", kinds)
	}
	res := r.lastOf(t, kindFailed).GetResult()
	if res.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_FAILURE || res.GetToolProtocolStatus() != "input_required" {
		t.Errorf("the execution ended %v %q, want FAILURE \"input_required\"", res.GetStatus(), res.GetToolProtocolStatus())
	}
}
