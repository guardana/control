package core_test

import (
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
)

// TestAClockBeforeTheEpochIsACauseInTheRequest: a clock reading before
// 1970-01-01T00:00:00Z, the zero time among them, is no "now" at all. The
// decision stops after the digest with POLICY_STALE as a cause in the request,
// so nothing is judged against it: no delegation expiry, no freshness, and no
// fail-open read, whatever the effect class, the bundle, or its absence.
func TestAClockBeforeTheEpochIsACauseInTheRequest(t *testing.T) {
	expired := readEnvelope()
	expired.Delegation = chain([]string{"read"}, base())
	cases := []struct {
		name string
		env  *controlv1.ActionEnvelope
		snap *policy.Snapshot
	}{
		{"a READ under a bundle confirmed at base", readEnvelope(), snapshot(t, allowReads)},
		{"a READ under a bundle confirmed at the zero time", readEnvelope(), snapshotAt(t, document(300, allowReads), time.Time{})},
		{"a READ with no bundle", readEnvelope(), nil},
		{"a WRITE under a bundle confirmed at base", writeEnvelope(), snapshot(t, allowWrites)},
		{"a WRITE under a bundle confirmed at the zero time", writeEnvelope(), snapshotAt(t, document(300, allowWrites), time.Time{})},
		{"a READ whose only hop expired at base", expired, snapshotAt(t, document(300, allowReads), time.Time{})},
	}
	clocks := []struct {
		name string
		now  time.Time
	}{
		{"the zero time", time.Time{}},
		{"one nanosecond before the epoch", time.Date(1969, time.December, 31, 23, 59, 59, 999999999, time.UTC)},
	}
	for _, clock := range clocks {
		k := kernel(t, failOpen(), fixedClock(clock.now))
		for _, c := range cases {
			out := decide(k, request(c.env), c.snap)
			check(t, out, expect{verdict: verdictIndeterminate, action: core.Block, codes: []string{codePolicyStale}})
			d := out.Decision
			if d.GetPolicyFreshness() != stale {
				t.Errorf("%s, %s: freshness %s, want STALE", clock.name, c.name, d.GetPolicyFreshness())
			}
			if d.GetActionDigest() == "" || len(d.GetPolicyRuleIds()) != 0 {
				t.Errorf("%s, %s: digest %q, rules %q; want the digest and no rule", clock.name, c.name, d.GetActionDigest(), d.GetPolicyRuleIds())
			}
		}
	}
}

// TestAClockAtTheEpochDecides is the accepting twin: the epoch itself is a
// usable reading, so a fresh bundle allows, an expired hop is a DENY, and a
// stale bundle opens a read under fail-open as anywhere else.
func TestAClockAtTheEpochDecides(t *testing.T) {
	epoch := time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	k := kernel(t, failOpen(), fixedClock(epoch))
	snap := snapshotAt(t, document(300, allowReads), epoch)
	check(t, decide(k, request(readEnvelope()), snap), expect{verdict: verdictAllow, action: core.Execute, codes: []string{codeRuleAllow}})
	expired := readEnvelope()
	expired.Delegation = chain([]string{"read"}, epoch)
	expired.Delegation[0].IssuedAt = timestampOf(epoch)
	check(t, decide(k, request(expired), snap), expect{verdict: verdictDeny, action: core.Block, codes: []string{codeDelegationExpired, codeRuleAllow}})
	check(t, decide(k, request(readEnvelope()), nil), expect{verdict: verdictIndeterminate, action: core.Execute, codes: []string{codePolicyUnavailable, codeFailOpenRead}})
}
