package policycompare

import (
	"context"
	"fmt"
	"slices"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/match"
)

// needsExternal is what a trace names for a rule that waits on the external
// decision point's answer.
const needsExternal = "external"

// loader loads one policy once for each instant a case loads it at.
type loader struct {
	policy Policy
	loads  map[instant]loaded
}

// instant is a time without its location, which no load reads.
type instant struct {
	sec  int64
	nsec int
}

type loaded struct {
	snap *policy.Snapshot
	err  error
}

func newLoader(p Policy) *loader {
	return &loader{policy: p, loads: map[instant]loaded{}}
}

func (l *loader) load(at time.Time) (*policy.Snapshot, error) {
	key := instant{at.Unix(), at.Nanosecond()}
	got, ok := l.loads[key]
	if !ok {
		got.snap, got.err = policy.Load(l.policy.Bundle, l.policy.Keys, at)
		l.loads[key] = got
	}
	return got.snap, got.err
}

// evaluate loads the policy at the case's loaded_at and decides the case at
// its decided_at through Kernel.Explain, which decides as the plane's Decide
// does. A side that cannot be loaded or decided, or on which no rule was
// evaluated, is not evaluated: its decision says nothing about the policy.
func evaluate(l *loader, in Input) side {
	snap, err := l.load(in.LoadedAt)
	if err != nil {
		return side{err: "the policy did not load: " + err.Error()}
	}
	kernel, err := core.New(in.Options, func() time.Time { return in.DecidedAt }, func() string { return "policy-compare" })
	if err != nil {
		return side{err: "the kernel refused the case's options: " + err.Error()}
	}
	out, why := kernel.Explain(context.Background(), core.Request{
		Envelope:       in.Envelope,
		Refusal:        in.Refusal,
		AuthorizedArgs: in.AuthorizedArgs,
		Flow:           in.Flow,
		External:       in.External,
	}, snap)
	d := out.Decision
	if d == nil {
		return side{err: "the kernel returned no decision"}
	}
	if why.Rules == nil {
		return side{err: fmt.Sprintf("no rule was evaluated: the kernel decided %s %v before the policy",
			verdictName(d.GetVerdict()), d.GetReasonCodes())}
	}
	s := side{
		evaluated:   true,
		verdict:     d.GetVerdict(),
		action:      out.Action,
		codes:       slices.Clone(d.GetReasonCodes()),
		obligations: d.GetObligations(),
		stale:       d.GetPolicyFreshness() != controlv1.PolicyFreshness_POLICY_FRESHNESS_FRESH,
	}
	for _, tr := range why.Rules {
		// A rule a false constraint decided reads no unknown into the
		// decision, whatever else it could not read.
		if tr.Value != match.ValueUnknown {
			continue
		}
		for _, u := range tr.Unknown {
			for _, need := range u.Needs {
				s.external = s.external || need == needsExternal
				if !slices.Contains(s.missing, need) {
					s.missing = append(s.missing, need)
				}
			}
		}
	}
	return s
}
