package core

import "github.com/guardana/control/internal/policy"

// checkClock is the step between the digest and the delegation chain. A
// reading policy.UsableTime refuses, or one earlier than the snapshot's
// NotBefore, a time the plane verified, is no time to judge an expiry or an
// age by. It stops the decision as a cause in the request, named POLICY_STALE
// because against it no bundle is known current; fail-open reads, a setting
// for a missing policy, never relieve it. A hop's issued_at is the producer's
// unsigned claim and proves nothing about this clock, so it is not read here.
func (d *decision) checkClock() bool {
	state := d.clockState()
	if d.explain != nil {
		d.explain.Clock = state
	}
	if state == ClockUsable {
		return true
	}
	d.add(codePolicyStale)
	d.causes.input = true
	return false
}

func (d *decision) clockState() ClockState {
	switch {
	case !policy.UsableTime(d.now):
		return ClockOutOfRange
	case d.now.Before(d.snap.NotBefore()):
		return ClockBeforeVerified
	}
	return ClockUsable
}
