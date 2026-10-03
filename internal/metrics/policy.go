package metrics

import (
	"errors"
	"slices"

	"github.com/guardana/control/internal/policywatch"
)

// PolicyRefresh is what the policy's refresher counted that /metrics answers:
// the polls it refused by cause, the polls that found one file of a pair,
// and the confirmations the clock rule withdrew.
type PolicyRefresh struct {
	Refused           map[policywatch.Cause]uint64
	AwaitingStatement uint64
	AwaitingBundle    uint64
	Withdrawals       uint64
}

// RefreshOf is the part of s /metrics answers.
func RefreshOf(s policywatch.Stats) PolicyRefresh {
	return PolicyRefresh{Refused: s.Refused, AwaitingStatement: s.AwaitingStatement, AwaitingBundle: s.AwaitingBundle, Withdrawals: s.Withdrawals}
}

// policyRows are how the policy stands and what its refresher refused.
var policyRows = []Metric{
	labelled(Gauge, "policy_freshness", "state", "PolicyFreshness",
		"1 for how the policy a call is decided under stands, confirmed, unconfirmed or expired, 0 for the two others; "+
			"while it is not confirmed every mode but OBSERVE blocks every material call.",
		func(r Reading) ([]sample, error) { return freshnessSamples(r.PolicyFreshness) }),
	level("policy_confirmation_seconds_left", "PolicySecondsLeft",
		"Whole seconds until the policy's confirmation expires; 0 while it is unconfirmed or expired.",
		func(r Reading) int64 { return r.PolicySecondsLeft }),
	labelled(Counter, "policy_refresh_refused_total", "cause", "Policy.Refused",
		"Polls that moved nothing because a replacement, a statement or the clock was refused, by cause; "+
			"a cause the refresher does not declare is counted under "+Other+".",
		func(r Reading) ([]sample, error) { return refreshSamples(r.Policy.Refused) }),
	count("policy_awaiting_statement_total", "Policy.AwaitingStatement",
		"Polls that found a new bundle whose statement still names another.",
		func(r Reading) uint64 { return r.Policy.AwaitingStatement }),
	count("policy_awaiting_bundle_total", "Policy.AwaitingBundle",
		"Polls that found a statement naming a bundle not yet on disk.",
		func(r Reading) uint64 { return r.Policy.AwaitingBundle }),
	count("policy_withdrawals_total", "Policy.Withdrawals",
		"Confirmations withdrawn because the wall clock stood more than a second below the highest it had read.",
		func(r Reading) uint64 { return r.Policy.Withdrawals }),
}

func refreshSamples(refused map[policywatch.Cause]uint64) ([]sample, error) {
	return closedSamples(refused, func(c policywatch.Cause) bool { return slices.Contains(policywatch.Causes(), c) })
}

func freshnessSamples(state policywatch.State) ([]sample, error) {
	if !slices.Contains(policywatch.States(), state) {
		return nil, errors.New("a freshness state outside the three")
	}
	out := make([]sample, 0, len(policywatch.States()))
	for _, s := range policywatch.States() {
		v := "0"
		if s == state {
			v = "1"
		}
		out = append(out, sample{s.String(), v})
	}
	return out, nil
}
