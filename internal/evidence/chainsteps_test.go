package evidence_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// prefixes reaches every state by a trail typed here, independently of the
// listing: the walk below appends each kind to each prefix and asks
// ValidateChain, so a listed edge is one the validator takes and a refused
// one is one it refuses. A refusal has to be the edge's own: the order
// check's "cannot follow", or the definite defect of a second proposal, which
// the validator reports before it consults the order; any other refusal means
// the walk tested something else, and fails. At the start of a trail a kind
// that leaves the state alone is followed by a proposal, because a trail that
// proposes nothing is refused whatever its edges.
var prefixes = map[string][]controlv1.EventKind{
	"the start of a trail":               {},
	"ACTION_PROPOSED":                    {proposed},
	"POLICY_DECIDED":                     {proposed, decided},
	"APPROVAL_REQUESTED":                 {proposed, decided, requested},
	"a decided approval":                 {proposed, decided, requested, answered},
	"an approval window nobody answered": {proposed, decided, requested, expired},
	"ACTION_STARTED":                     {proposed, decided, started},
	"a closed action":                    {proposed, decided, started, completed},
}

func TestChainStepsAgreeWithValidateChain(t *testing.T) {
	steps := evidence.ChainSteps()
	kinds := controlv1.EventKind(0).Descriptor().Values().Len()
	// Every declared kind but UNSPECIFIED, plus one undeclared, per state.
	if want := len(prefixes) * kinds; len(steps) != want {
		t.Fatalf("%d steps, want %d", len(steps), want)
	}
	states := map[string]int{}
	for _, s := range steps {
		states[s.From]++
		prefix, ok := prefixes[s.From]
		if !ok {
			t.Fatalf("the listing names a state this test cannot reach: %q", s.From)
		}
		kinds := append(append([]controlv1.EventKind(nil), prefix...), s.Kind)
		if len(prefix) == 0 && s.Kind != proposed && s.Allowed {
			kinds = append(kinds, proposed)
		}
		if problem := edgeProblem(s, evidence.ValidateChain(trailOfKinds(t, kinds...))); problem != "" {
			t.Errorf("%s + %s: %s", s.From, s.Kind, problem)
		}
	}
	for state := range prefixes {
		if states[state] != kinds {
			t.Errorf("%q has %d steps, want %d", state, states[state], kinds)
		}
	}
}

// edgeProblem compares one listed edge with the validator's answer to it.
func edgeProblem(s evidence.ChainStep, err error) string {
	switch {
	case s.Unplaceable:
		if s.Declared || !errors.Is(err, evidence.ErrChainIndeterminate) {
			return fmt.Sprintf("listed unplaceable, ValidateChain says %v", err)
		}
	case s.Allowed:
		if err != nil {
			return fmt.Sprintf("listed allowed, ValidateChain refuses: %v", err)
		}
	case err == nil:
		return "listed refused, ValidateChain accepts"
	case !strings.Contains(err.Error(), "cannot follow") && !strings.Contains(err.Error(), "already proposed"):
		return fmt.Sprintf("refused for something other than the edge: %v", err)
	}
	return ""
}

// The edges ADR-0013 rests on, typed from the record: a window that closed
// unanswered may be asked again and never started from; a block may follow
// it; nothing precedes a proposal.
func TestChainStepsNameTheRecordsEdges(t *testing.T) {
	edges := map[[2]string]bool{}
	for _, s := range evidence.ChainSteps() {
		edges[[2]string{s.From, s.Kind.String()}] = s.Allowed
	}
	for name, c := range map[string]struct {
		from string
		kind controlv1.EventKind
		want bool
	}{
		"an expired window asked again":    {"an approval window nobody answered", requested, true},
		"an expired window never starts":   {"an approval window nobody answered", started, false},
		"an expired window may be blocked": {"an approval window nobody answered", blocked, true},
		"a decided approval starts":        {"a decided approval", started, true},
		"nothing precedes a proposal":      {"the start of a trail", decided, false},
		"a finding needs a trail":          {"the start of a trail", found, false},
		"a reload fits anywhere":           {"the start of a trail", reloaded, true},
	} {
		if got, ok := edges[[2]string{c.from, c.kind.String()}]; !ok || got != c.want {
			t.Errorf("%s: listed %v (present %v), want %v", name, got, ok, c.want)
		}
	}
}
