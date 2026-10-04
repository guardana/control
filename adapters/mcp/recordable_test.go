package mcp_test

import (
	"strings"
	"testing"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/pkg/contract"
)

// TestAnOverlongResourceKeepsTheClassifiedName: a classified call whose
// resource, read from the agent's arguments, is past the contract's bound is
// refused as too large; the trail records it under the tool's own name, and
// without the resource the agent chose.
func TestAnOverlongResourceKeepsTheClassifiedName(t *testing.T) {
	r := newRig(t, mcp.KindStdio, rigOptions{})
	agent := r.connect(t, "agent-a")
	path := strings.Repeat("p", contract.MaxStringBytes+1)
	_, _ = callTool(t, agent, "read_file", map[string]any{"path": path})
	got := r.pipe.admitted()
	if len(got) != 1 {
		t.Fatalf("%d admission(s), want 1", len(got))
	}
	a := got[0].a
	expectOffTheTrail(t, a, "an overlong resource")
	if name := a.Envelope.GetAction().GetName(); name != "read_file" {
		t.Errorf("the refused call is recorded as %q, want read_file", name)
	}
	if id := a.Envelope.GetResource().GetId(); id != "" {
		t.Errorf("the refused call keeps a resource of %d bytes, want none", len(id))
	}
	if n := len(a.Envelope.GetContext().GetTags()); n != 2 {
		t.Errorf("the refused call keeps %d tag(s), want the client's and the revision's", n)
	}
}
