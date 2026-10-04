package mcp_test

import (
	"bufio"
	"context"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

const (
	traceA      = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanA       = "00f067aa0ba902b7"
	traceparent = "00-" + traceA + "-" + spanA + "-01"
)

// TestACallNamesTheTraceItsClientGave: a traceparent in a call's _meta
// lands in the envelope's trace_id and span_id on every listener kind; a call
// without one, or with a malformed one, records both empty; and none of the
// four changes the action digest.
func TestACallNamesTheTraceItsClientGave(t *testing.T) {
	const traceB, spanB = "0af7651916cd43dd8448eb211c80319c", "b7ad6b7169203331"
	calls := []struct {
		meta        sdk.Meta
		trace, span string
	}{
		{sdk.Meta{"traceparent": traceparent}, traceA, spanA},
		{nil, "", ""},
		{sdk.Meta{"traceparent": strings.ToUpper(traceparent)}, "", ""},
		{sdk.Meta{"traceparent": "00-" + traceB + "-" + spanB + "-00"}, traceB, spanB},
	}
	forEachKind(t, rigOptions{}, func(t *testing.T, r *rig) {
		agent := r.connect(t, "agent-a")
		// The client writes its protocol version into the _meta it is given.
		for _, c := range calls {
			if _, err := agent.CallTool(ctxT(t), &sdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "/a"}, Meta: maps.Clone(c.meta)}); err != nil {
				t.Fatalf("call with _meta %v: %v", c.meta, err)
			}
		}
		admitted := r.pipe.admitted()
		if len(admitted) != len(calls) {
			t.Fatalf("%d admissions, want %d", len(admitted), len(calls))
		}
		var digests []string
		for i, c := range calls {
			env := admitted[i].a.Envelope
			if env.GetTraceId() != c.trace || env.GetSpanId() != c.span {
				t.Errorf("call %d with _meta %v recorded trace %q span %q, want %q %q", i, c.meta, env.GetTraceId(), env.GetSpanId(), c.trace, c.span)
			}
			d, err := canon.DigestV1(env, admitted[i].a.Arguments)
			if err != nil {
				t.Fatalf("DigestV1: %v", err)
			}
			digests = append(digests, d)
		}
		for i, d := range digests[1:] {
			if d != digests[0] {
				t.Errorf("call %d digests to %s, call 0 to %s: trace context moved the digest", i+1, d, digests[0])
			}
		}
	})
}

// TestAReadNamesTheTraceItsClientGave: a resources/read and a prompts/get
// carry the trace context as a tools/call does.
func TestAReadNamesTheTraceItsClientGave(t *testing.T) {
	forEachKind(t, rigOptions{}, func(t *testing.T, r *rig) {
		agent := r.connect(t, "agent-a")
		if _, err := agent.ReadResource(ctxT(t), &sdk.ReadResourceParams{URI: "file:///r", Meta: sdk.Meta{"traceparent": traceparent}}); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.GetPrompt(ctxT(t), &sdk.GetPromptParams{Name: "p", Arguments: map[string]string{"q": "x"}, Meta: sdk.Meta{"traceparent": traceparent}}); err != nil {
			t.Fatal(err)
		}
		admitted := r.pipe.admitted()
		if len(admitted) != 2 {
			t.Fatalf("%d admissions, want 2", len(admitted))
		}
		for _, a := range admitted {
			if env := a.a.Envelope; env.GetTraceId() != traceA || env.GetSpanId() != spanA {
				t.Errorf("%s recorded trace %q span %q", env.GetAction().GetKind(), env.GetTraceId(), env.GetSpanId())
			}
		}
	})
}

// TestTheTrailKeepsTheTrace: on the real pipeline the trace context reaches
// the trail in the proposed envelope, and the call runs as it would without.
func TestTheTrailKeepsTheTrace(t *testing.T) {
	r := newRig(t, mcp.KindStdio, rigOptions{mode: modeEnforce, rules: []string{allowEverything}})
	agent := r.connect(t, "agent-a")
	res, err := agent.CallTool(ctxT(t), &sdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "/a"}, Meta: sdk.Meta{"traceparent": traceparent}})
	if err != nil || res.IsError || r.victim.count("read_file") != 1 {
		t.Fatalf("the call did not run: %v %+v", err, res)
	}
	var proposed []*controlv1.ActionEnvelope
	for _, e := range r.events() {
		if e.GetKind() == controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED {
			proposed = append(proposed, e.GetProposed())
		}
	}
	if len(proposed) != 1 || proposed[0].GetTraceId() != traceA || proposed[0].GetSpanId() != spanA {
		t.Fatalf("the trail proposed %v", proposed)
	}
}

// TestAListWithoutParamsIsAnswered: a tools/list with no params, or null
// params, is answered under list shaping, whose preview builds an envelope
// for each tool: raw on the stateless listener and on stdio, and from the
// SDK's own client on a stateful session.
func TestAListWithoutParamsIsAnswered(t *testing.T) {
	bodies := []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":null}`,
	}
	for _, shaping := range []mcp.Shaping{mcp.ShapeAnnotate, mcp.ShapeHide} {
		r := newRig(t, mcp.KindStatelessHTTP, rigOptions{shaping: shaping, mode: modeEnforce, rules: []string{allowEverything}})
		for _, body := range bodies {
			code, raw := rawPost(t, r.url, http.Header{"Mcp-Protocol-Version": {v20251125}}, body)
			if code != http.StatusOK || !strings.Contains(string(raw), `"tools"`) {
				t.Errorf("stateless, shaping %v, %s: %d %s", shaping, body, code, raw)
			}
		}
		r = newRig(t, mcp.KindStdio, rigOptions{shaping: shaping, mode: modeEnforce, rules: []string{allowEverything}})
		for _, body := range bodies {
			if raw := stdioExchange(t, r.adapter, body); !strings.Contains(raw, `"tools"`) {
				t.Errorf("stdio, shaping %v, %s: %s", shaping, body, raw)
			}
		}
		// The SDK's own client lists with no params at all.
		r = newRig(t, mcp.KindStatefulHTTP, rigOptions{shaping: shaping, mode: modeEnforce, rules: []string{allowEverything}})
		if _, err := r.connect(t, "agent-a").ListTools(ctxT(t), nil); err != nil {
			t.Errorf("stateful, shaping %v: %v", shaping, err)
		}
	}
}

// TestACallWithoutParamsIsRefusedBeforeTheAdapter: the adapter reads the
// params of a tools/call, a resources/read and a prompts/get without a nil
// check, because the SDK refuses those methods with params absent or null
// before any middleware runs: a 400 or a JSON-RPC error answers each, and the
// listener answers the next request.
func TestACallWithoutParamsIsRefusedBeforeTheAdapter(t *testing.T) {
	var bodies []string
	for _, method := range []string{"tools/call", "resources/read", "prompts/get"} {
		bodies = append(bodies,
			`{"jsonrpc":"2.0","id":2,"method":"`+method+`"}`,
			`{"jsonrpc":"2.0","id":2,"method":"`+method+`","params":null}`)
	}
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{})
	for _, version := range []string{v20251125, v20260728} {
		for _, body := range bodies {
			if code, raw := rawPost(t, r.url, http.Header{"Mcp-Protocol-Version": {version}}, body); code != http.StatusBadRequest && !strings.Contains(string(raw), `"error"`) {
				t.Errorf("stateless %s, %s: %d %s", version, body, code, raw)
			}
		}
	}
	r = newRig(t, mcp.KindStdio, rigOptions{})
	for _, body := range bodies {
		if raw := stdioExchange(t, r.adapter, body); !strings.Contains(raw, `"error"`) {
			t.Errorf("stdio, %s: %s", body, raw)
		}
	}
	if raw := stdioExchange(t, r.adapter, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); !strings.Contains(raw, `"tools"`) {
		t.Errorf("the listener does not answer after the refusals: %s", raw)
	}
	if r.pipe != nil && len(r.pipe.admitted()) != 0 {
		t.Errorf("a call without params reached the pipeline: %d admissions", len(r.pipe.admitted()))
	}
}

// stdioExchange initializes a stdio listener over a new pipe and returns the
// answer to one more request.
func stdioExchange(t *testing.T, a *mcp.Adapter, body string) string {
	t.Helper()
	toServer, fromAgent := io.Pipe()
	toAgent, fromServer := io.Pipe()
	ctx, cancel := context.WithTimeout(ctxT(t), 5*time.Second)
	t.Cleanup(cancel)
	go func() { _ = a.ServeStdio(ctx, toServer, fromServer) }()
	t.Cleanup(func() { _ = fromAgent.Close(); _ = toAgent.Close() })
	lines := make(chan string, 2)
	go func() {
		sc := bufio.NewScanner(toAgent)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent-b","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		body,
	} {
		if _, err := io.WriteString(fromAgent, line+"\n"); err != nil {
			t.Fatalf("writing %s: %v", line, err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case l := <-lines:
			if i == 1 {
				return l
			}
		case <-ctx.Done():
			t.Fatalf("no answer to %s", body)
		}
	}
	return ""
}

// TestAHeldCallResumesUnderAnotherTrace: a retry carries a new span, so each
// attempt at a held call names another trace context. A waiting retry under
// another trace is not held anew, the approved retry under a third runs once
// on the held trail, and that trail keeps the trace of the attempt that was
// held.
func TestAHeldCallResumesUnderAnotherTrace(t *testing.T) {
	r := newRig(t, mcp.KindStdio, rigOptions{mode: modeApprove, rules: []string{allowDeletes}})
	agent := r.connect(t, "agent-a")
	if res := deleteUnder(t, agent, traceparent); res.Meta[metaAnswer] != "pending" {
		t.Fatalf("the first attempt was not held: %+v", res)
	}
	requested := r.lastOf(t, kindRequested)
	recorded := len(r.events())
	if res := deleteUnder(t, agent, "00-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-bbbbbbbbbbbbbbbb-01"); res.Meta[metaAnswer] != "pending" {
		t.Fatalf("the waiting retry was not pending: %+v", res)
	}
	if n := len(r.events()); n != recorded {
		t.Fatalf("a retry under another trace was held anew: %v", r.kindsOf(t, ""))
	}
	if err := r.approvals.Answer(requested.GetApproval().GetApprovalId(), controlv1.ApprovalState_APPROVAL_STATE_APPROVED, "alice", "", time.Now()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res := deleteUnder(t, agent, "00-cccccccccccccccccccccccccccccccc-cccccccccccccccc-01"); res.IsError || r.victim.count("delete_file") != 1 {
		t.Fatalf("the approved retry under a third trace did not run: %+v", res)
	}
	proposed := r.proposedOn(requested.GetRequestId())
	if len(proposed) != 1 || proposed[0].GetTraceId() != traceA || proposed[0].GetSpanId() != spanA {
		t.Fatalf("the held trail proposed %v, want one envelope under the held attempt's trace", proposed)
	}
	if got := r.kindsOf(t, requested.GetRequestId()); got[len(got)-1] != kindCompleted {
		t.Fatalf("the held trail ends %v", got)
	}
}

// deleteUnder calls delete_file on /x under the trace context tp.
func deleteUnder(t *testing.T, agent *sdk.ClientSession, tp string) *sdk.CallToolResult {
	t.Helper()
	res, err := agent.CallTool(ctxT(t), &sdk.CallToolParams{Name: "delete_file", Arguments: map[string]any{"path": "/x"}, Meta: sdk.Meta{"traceparent": tp}})
	if err != nil {
		t.Fatalf("call under %s: %v", tp, err)
	}
	return res
}

// proposedOn is every envelope the trail of requestID proposed.
func (r *rig) proposedOn(requestID string) []*controlv1.ActionEnvelope {
	var out []*controlv1.ActionEnvelope
	for _, e := range r.events() {
		if e.GetRequestId() == requestID && e.GetProposed() != nil {
			out = append(out, e.GetProposed())
		}
	}
	return out
}
