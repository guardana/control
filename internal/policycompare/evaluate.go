package policycompare

import (
	"context"
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

// evaluate loads p at the case's loaded_at and decides the case at its
// decided_at through Kernel.Explain, which decides as the plane's Decide
// does. A side that cannot be loaded or decided is not evaluated.
func evaluate(p Policy, in Input) side {
	snap, err := policy.Load(p.Bundle, p.Keys, in.LoadedAt)
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
