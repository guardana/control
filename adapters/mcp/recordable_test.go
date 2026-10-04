package mcp_test

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

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

// TestEveryOverlongAgentStringIsTakenOff: a size refusal takes every string
// the agent chose past the bound off the recorded proposal, not only the one
// Validate named first, so the proposal holds no size refusal of its own; a
// classified name and a tag within the bound stay.
func TestEveryOverlongAgentStringIsTakenOff(t *testing.T) {
	overlong := strings.Repeat("p", contract.MaxStringBytes+1)
	for name, c := range map[string]struct {
		client   string
		call     func(t *testing.T, agent *sdk.ClientSession)
		wantName string
		wantTags int
	}{
		"a client name and a resource": {strings.Repeat("c", contract.MaxStringBytes+1), func(t *testing.T, agent *sdk.ClientSession) {
			_, _ = callTool(t, agent, "read_file", map[string]any{"path": overlong})
		}, "read_file", 1},
		"a read's URI as name and resource": {"agent-a", func(t *testing.T, agent *sdk.ClientSession) {
			_, _ = agent.ReadResource(ctxT(t), &sdk.ReadResourceParams{URI: "file:///" + overlong})
		}, "", 2},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, mcp.KindStdio, rigOptions{})
			c.call(t, r.connect(t, c.client))
			got := r.pipe.admitted()
			if len(got) != 1 {
				t.Fatalf("%d admission(s), want 1", len(got))
			}
			a := got[0].a
			expectOffTheTrail(t, a, name)
			env := a.Envelope
			if err := contract.Validate(env); errors.Is(err, contract.ErrTooLarge) {
				t.Errorf("the recorded proposal is still refused for size: %v", err)
			}
			if got := env.GetAction().GetName(); got != c.wantName {
				t.Errorf("the refused call is recorded as %q, want %q", got, c.wantName)
			}
			if id := env.GetResource().GetId(); id != "" {
				t.Errorf("the refused call keeps a resource of %d bytes, want none", len(id))
			}
			if n := len(env.GetContext().GetTags()); n != c.wantTags {
				t.Errorf("the refused call keeps %d tag(s), want %d", n, c.wantTags)
			}
		})
	}
}
