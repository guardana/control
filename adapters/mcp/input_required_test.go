package mcp_test

import (
	"context"
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

// askingVictim is a victim whose read_file, resource and prompt answer
// input_required with requests, and which counts every call, read and prompt
// that reaches it.
func askingVictim(requests func() sdk.InputRequestMap) (*victim, func(string) int) {
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
	v.replaced["read_file"] = func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{InputRequests: requests()}, nil
	}
	v.server.AddResource(&sdk.Resource{URI: "file:///r", Name: "r"}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{InputRequests: requests()}, nil
	})
	v.server.AddPrompt(&sdk.Prompt{Name: "p"}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{InputRequests: requests()}, nil
	})
	return v, func(m string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[m]
	}
}

// TestInputRequiredIsSentOnceAndFails: an upstream answering a call, a
// read or a prompt with input_required, for load shedding or with a roots
// request the client could fulfil itself, receives the one request the plane
// admitted. The agent is told the call failed with a fixed answer, nothing of
// the upstream's requests is relayed, and the trail holds one execution that
// ended failed.
func TestInputRequiredIsSentOnceAndFails(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requests func() sdk.InputRequestMap
	}{
		{"empty", func() sdk.InputRequestMap { return sdk.InputRequestMap{} }},
		{"roots", func() sdk.InputRequestMap { return sdk.InputRequestMap{"r1": &sdk.ListRootsParams{}} }}, //nolint:staticcheck // deprecated, yet an upstream can still ask for roots
	} {
		for _, m := range []struct {
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
		} {
			t.Run(tc.name+"/"+m.method, func(t *testing.T) {
				v, seen := askingVictim(tc.requests)
				r := newRigOver(t, v, mcp.KindStdio, rigOptions{mode: modeEnforce, rules: []string{allowReads}})
				err := m.do(ctxT(t), r.connect(t, "agent-a"))
				if n := seen(m.method); n != 1 {
					t.Errorf("the upstream received %d %s request(s) for one admitted call, want 1", n, m.method)
				}
				var werr *jsonrpc.Error
				if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeInternalError || werr.Message != messageInputRequired {
					t.Errorf("the agent was answered %v, want code %d and %q", err, jsonrpc.CodeInternalError, messageInputRequired)
				}
				wantOneFailedExecution(t, r)
			})
		}
	}
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
