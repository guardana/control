package e2e_test

import (
	"slices"
	"testing"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestAnUnconfirmedPlaneBlocksMaterialCalls: a plane whose bundle no statement
// confirms runs, and decides every call stale (ADR-0038). A material call is
// blocked with POLICY_STALE and never reaches the upstream; a read is blocked
// too, unless fail_open_read lets it run, and then the decision says so.
func TestAnUnconfirmedPlaneBlocksMaterialCalls(t *testing.T) {
	writes := `{"id":"allow-writes","effect":"ALLOW","when":{"action":{"effect":["WRITE"]}}}`
	toolWrite := classTool(controlv1.EffectClass_EFFECT_CLASS_WRITE)
	for _, c := range []struct {
		name         string
		tool         string
		failOpenRead bool
		runs         bool
	}{
		{"a write", toolWrite, false, false},
		{"a write under fail_open_read", toolWrite, true, false},
		{"a delete", toolDelete, false, false},
		{"a read", toolRead, false, false},
		{"a read under fail_open_read", toolRead, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, unconfirmed: true,
				rules: []string{allowReads, allowDeletes, writes}, failOpenRead: c.failOpenRead})
			agent := p.connect(t, "agent-a")
			res := call(t, agent, c.tool, map[string]any{"path": "/x"})
			trail := p.oneTrail(t)
			if c.runs {
				if res.IsError {
					t.Fatalf("a read under fail_open_read came back as a refusal: %+v", res.StructuredContent)
				}
				expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
			} else {
				blockedWith(t, res, codePolicyStale)
				expectTrail(t, trail, kindProposed, kindDecided, kindBlocked)
			}
			if n, want := p.victim.count(c.tool), map[bool]int{true: 1, false: 0}[c.runs]; n != want {
				t.Errorf("the upstream ran %d time(s), want %d", n, want)
			}
			decision := eventOf(t, trail, kindDecided).GetDecision()
			codes := decision.GetReasonCodes()
			if !slices.Contains(codes, codePolicyStale) {
				t.Errorf("the recorded decision says %v, without %s", codes, codePolicyStale)
			}
			if opened := slices.Contains(codes, codeFailOpenRead); opened != c.runs {
				t.Errorf("the recorded decision says %v; %s among them is %t, want %t", codes, codeFailOpenRead, opened, c.runs)
			}
			if loaded := decision.GetPolicyLoadedAt().AsTime(); !loaded.IsZero() {
				t.Errorf("an unconfirmed snapshot's decision says it was confirmed at %v, want the zero time", loaded)
			}
		})
	}
}

// TestAConfirmedPlaneRunsTheSameCalls is the pair of the test above: the same
// rules on a plane confirmed now run the write and the read, so it is the
// missing statement, and nothing else, that blocks them there.
func TestAConfirmedPlaneRunsTheSameCalls(t *testing.T) {
	writes := `{"id":"allow-writes","effect":"ALLOW","when":{"action":{"effect":["WRITE"]}}}`
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, writes}})
	agent := p.connect(t, "agent-a")
	for _, tool := range []string{classTool(controlv1.EffectClass_EFFECT_CLASS_WRITE), toolRead} {
		if res := call(t, agent, tool, map[string]any{"path": "/x"}); res.IsError {
			t.Fatalf("%s on a confirmed plane came back as a refusal: %+v", tool, res.StructuredContent)
		}
	}
}
