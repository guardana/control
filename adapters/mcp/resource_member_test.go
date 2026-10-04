package mcp_test

import (
	"slices"
	"testing"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

// TestARewriteInsideAnUnreadableResourceMemberIsNotSent: a resource member
// that is an object or a boolean names no resource on either side, so the
// rewritten bytes are held to the member the agent proposed; a rewrite inside
// it aborts, and one that keeps it whole, spelled another way, is sent.
func TestARewriteInsideAnUnreadableResourceMemberIsNotSent(t *testing.T) {
	r := newRig(t, mcp.KindStdio, rigOptions{})
	agent := r.connect(t, "agent-a")
	for name, c := range map[string]struct {
		proposed   any
		authorized string
	}{
		"an object": {map[string]any{"a": "/x"}, `{"path":{"a":"/etc/shadow"}}`},
		"a boolean": {true, `{"path":false}`},
		"a list":    {[]any{"/x"}, `{"path":["/etc/shadow"]}`},
		"absent":    {nil, `{"path":{"a":"/etc/shadow"}}`},
	} {
		r.pipe.decide = authorize(c.authorized)
		args := map[string]any{"path": c.proposed}
		if c.proposed == nil {
			args = map[string]any{"other": 1}
		}
		res, err := callTool(t, agent, "read_file", args)
		if err != nil || !res.IsError || !slices.Contains(codesOf(t, res), "OBLIGATION_NOT_UNDERSTOOD") {
			t.Errorf("%s: a rewrite inside the member was answered %v %+v", name, err, res)
		}
	}
	if n := r.victim.count("read_file"); n != 0 {
		t.Errorf("the victim ran read_file %d time(s) on a rewritten member", n)
	}
	for _, a := range r.pipe.aborted() {
		if a.cause != gateway.AbortObligation {
			t.Errorf("an abort for %v, want one for an obligation", a.cause)
		}
	}
	r.pipe.decide = authorize(`{"path":{"b":2.0,"a":"/x"},"extra":1}`)
	if res, err := callTool(t, agent, "read_file", map[string]any{"path": map[string]any{"a": "/x", "b": 2}}); err != nil || res.IsError {
		t.Fatalf("a rewrite that keeps the member: %v %+v", err, res)
	}
	if n := r.victim.count("read_file"); n != 1 {
		t.Errorf("the victim ran read_file %d time(s), want the kept member sent once", n)
	}
}
