package e2e_test

import (
	"io"
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/evidence"
)

func newPipe() (io.ReadCloser, io.WriteCloser) {
	r, w := io.Pipe()
	return r, w
}

// eventually fails the test unless want becomes true within the bound. It is
// for what another goroutine does on its own schedule, the exporter's delivery
// and the upstream's slow call; nothing the caller can synchronise on uses it.
func eventually(t *testing.T, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !want() {
		if time.Now().After(deadline) {
			t.Fatal("the condition never came true within the bound")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// argumentsHash is canon.ArgumentsHashV1 of args, the hash an envelope carries
// for the bytes it is about.
func argumentsHash(t *testing.T, args string) string {
	t.Helper()
	hash, err := canon.ArgumentsHashV1([]byte(args))
	if err != nil {
		t.Fatalf("ArgumentsHashV1(%s): %v", args, err)
	}
	return hash
}

func callTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, error) {
	t.Helper()
	return cs.CallTool(ctxT(t), &sdk.CallToolParams{Name: name, Arguments: args})
}

// call is one tools/call that has to reach the gateway: a transport error is
// the test's own failure, and what the gateway answered is the result.
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := callTool(t, cs, name, args)
	if err != nil {
		t.Fatalf("%s: the gateway answered an error instead of a result: %v", name, err)
	}
	return res
}

// allowed is one call that has to have run: not an error, not an isError
// result.
func allowed(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res := call(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s came back as a refusal: %+v", name, res.StructuredContent)
	}
	return res
}

func structured(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent is %T, want an object: %+v", res.StructuredContent, res)
	}
	return m
}

// codesOf is the reason codes of a block, as the agent reads them.
func codesOf(t *testing.T, res *sdk.CallToolResult) []string {
	t.Helper()
	raw, ok := structured(t, res)["reason_codes"].([]any)
	if !ok {
		t.Fatalf("no reason_codes in %v", res.StructuredContent)
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		out = append(out, c.(string))
	}
	return out
}

// blockedWith checks the answer is the refusal an agent can read: isError,
// with code among the reason codes it carries.
func blockedWith(t *testing.T, res *sdk.CallToolResult, code string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("a refusal came back as a success: %+v", res)
	}
	if got := codesOf(t, res); !slices.Contains(got, code) {
		t.Fatalf("the block says %v, want %s among them", got, code)
	}
}

// trails groups every recorded event by the request it belongs to, in the
// order the requests first appear.
func (p *plane) trails() ([]string, map[string][]*controlv1.Event) {
	var order []string
	byRequest := map[string][]*controlv1.Event{}
	for _, e := range p.sink.events() {
		id := e.GetRequestId()
		if _, seen := byRequest[id]; !seen {
			order = append(order, id)
		}
		byRequest[id] = append(byRequest[id], e)
	}
	return order, byRequest
}

// oneTrail is the recorded trail when the plane recorded exactly one request,
// which is what a single-call test arranges.
func (p *plane) oneTrail(t *testing.T) []*controlv1.Event {
	t.Helper()
	order, byRequest := p.trails()
	if len(order) != 1 {
		t.Fatalf("the plane recorded %d trails, want 1: %v", len(order), order)
	}
	return byRequest[order[0]]
}

func kindsOf(events []*controlv1.Event) []controlv1.EventKind {
	out := make([]controlv1.EventKind, 0, len(events))
	for _, e := range events {
		out = append(out, e.GetKind())
	}
	return out
}

// expectTrail checks a trail's kinds in order and that the chain validates:
// the hash links, the identifiers and the grammar of what may follow what.
func expectTrail(t *testing.T, events []*controlv1.Event, want ...controlv1.EventKind) {
	t.Helper()
	if got := kindsOf(events); !slices.Equal(got, want) {
		t.Fatalf("the trail is %v, want %v", got, want)
	}
	if err := evidence.ValidateChain(events); err != nil {
		t.Fatalf("ValidateChain over the trail: %v", err)
	}
}

// eventOf is the one event of kind in events.
func eventOf(t *testing.T, events []*controlv1.Event, kind controlv1.EventKind) *controlv1.Event {
	t.Helper()
	var found *controlv1.Event
	for _, e := range events {
		if e.GetKind() != kind {
			continue
		}
		if found != nil {
			t.Fatalf("%s appears more than once in the trail", kind)
		}
		found = e
	}
	if found == nil {
		t.Fatalf("no %s in the trail %v", kind, kindsOf(events))
	}
	return found
}
