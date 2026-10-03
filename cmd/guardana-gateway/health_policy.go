package main

import (
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policywatch"
)

// policyAnswer is the policy's freshness: confirmed, unconfirmed or expired,
// the statement's issuedAt and when its budget ends where there is one, the
// whole seconds left while it is confirmed, and what the refresher refused,
// awaited and withdrew.
type policyAnswer struct {
	Freshness   string         `json:"freshness"`
	ConfirmedAt string         `json:"confirmed_at,omitempty"`
	ExpiresAt   string         `json:"expires_at,omitempty"`
	SecondsLeft int64          `json:"seconds_left"`
	Refresh     map[string]any `json:"refresh,omitempty"`
}

// freshness is how snap stands at now, when its confirmation expires and the
// whole seconds left before it does, 0 unless it is confirmed.
func (p *plane) freshness(snap *policy.Snapshot, now time.Time) (policywatch.State, time.Time, int64) {
	state, expires := policywatch.Freshness(snap, p.cfg.Policy.MaxStale, now)
	if state != policywatch.Confirmed {
		return state, expires, 0
	}
	return state, expires, int64(expires.Sub(now) / time.Second)
}

// policyAnswer is snap's freshness at now and the refresher's counts.
func (p *plane) policyAnswer(snap *policy.Snapshot, now time.Time) policyAnswer {
	state, expires, left := p.freshness(snap, now)
	out := policyAnswer{Freshness: state.String(), SecondsLeft: left}
	if state != policywatch.Unconfirmed {
		out.ConfirmedAt = snap.ConfirmedAt().UTC().Format(time.RFC3339)
		out.ExpiresAt = expires.UTC().Format(time.RFC3339)
	}
	if p.policy != nil && p.policy.refresher != nil {
		s := p.policy.refresher.Stats()
		refused := make(map[string]uint64, len(s.Refused))
		for cause, n := range s.Refused {
			refused[string(cause)] = n
		}
		out.Refresh = map[string]any{
			"refused": refused, "awaiting_statement": s.AwaitingStatement,
			"awaiting_bundle": s.AwaitingBundle, "withdrawals": s.Withdrawals,
		}
	}
	return out
}
