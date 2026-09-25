package delegation_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core/delegation"
)

// Small pools, so that a party met twice, a scope outside the parent's and an
// expiry at the clock are common draws rather than rare ones.
var (
	partyPool = []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"}
	scopePool = []string{"s0", "s1", "s2", "s3", "s4"}
)

// TestCheckAgreesWithTheRulesOnAnyChain holds Check to a brute-force reading
// of the delegation rules (ADR-0012) over chains of every shape, those
// Validate would refuse included: nil hops, missing and unrepresentable
// expiries, hops that do not link. Check has to pass exactly the chains the
// reading allows, grant the last hop's scopes when it does, and grant nothing
// when it does not.
func TestCheckAgreesWithTheRulesOnAnyChain(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := drawAnyChain(t)
		got, err := delegation.Check(c, clock())
		switch {
		case len(c) == 0:
			if err != nil || got.Delegated || got.Scopes != nil {
				t.Fatalf("no chain gave %+v, %v", got, err)
			}
		case allowedByTheRules(c, clock()):
			if err != nil {
				t.Fatalf("Check refused a chain the rules allow: %v", err)
			}
			if !got.Delegated || !slices.Equal(got.Scopes, c[len(c)-1].GetScopes()) {
				t.Fatalf("Check gave %+v, want the last hop's scopes %q", got, c[len(c)-1].GetScopes())
			}
		default:
			assertRefusedWithAnyCode(t, got, err)
		}
	})
}

// assertRefusedWithAnyCode holds a refusal to one of the three codes and to
// granting nothing.
func assertRefusedWithAnyCode(t *rapid.T, got delegation.Effective, err error) {
	var refusal *delegation.Error
	if !errors.As(err, &refusal) || !slices.Contains([]string{expired, cycle, exceedsParent}, refusal.Code) {
		t.Fatalf("Check gave %v for a chain the rules refuse, want one of the three codes", err)
	}
	if got.Delegated || got.Scopes != nil {
		t.Fatalf("a refusal returned %+v, want the zero Effective", got)
	}
}

// TestCheckNamesTheMostUrgentFaultPutIn starts from a chain the rules allow and
// puts in a drawn set of faults, each at a drawn hop. The code expected is
// written out per fault; with several, it is the first of expired, cycle,
// beyond the parent, the order TestCheckNamesOneFaultInAFixedOrder pins by
// example. With none, the chain passes and grants its last hop's scopes.
func TestCheckNamesTheMostUrgentFaultPutIn(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := drawNarrowingChain(t, 2)
		faults := rapid.IntRange(0, 7).Draw(t, "faults")
		want := ""
		if faults&1 != 0 {
			widenOneHop(t, c)
			want = exceedsParent
		}
		if faults&2 != 0 {
			returnToAnEarlierParty(t, c)
			want = cycle
		}
		if faults&4 != 0 {
			expireOneHop(t, c)
			want = expired
		}

		got, err := delegation.Check(c, clock())
		if want == "" {
			if err != nil || !got.Delegated || !slices.Equal(got.Scopes, c[len(c)-1].GetScopes()) {
				t.Fatalf("a chain the rules allow gave %+v, %v", got, err)
			}
			return
		}
		var refusal *delegation.Error
		if !errors.As(err, &refusal) || refusal.Code != want {
			t.Fatalf("Check gave %v, want %s", err, want)
		}
		if got.Delegated || got.Scopes != nil {
			t.Fatalf("a refusal returned %+v, want the zero Effective", got)
		}
	})
}

// drawNarrowingChain draws a chain the rules allow: each hop continues the one
// before, no party is met twice, every expiry is after the clock, and each
// hop's scopes are drawn from its parent's, in a drawn order.
func drawNarrowingChain(t *rapid.T, minHops int) []*controlv1.Delegation {
	n := rapid.IntRange(minHops, 8).Draw(t, "hops")
	named := rapid.Permutation(partyPool).Draw(t, "parties")
	held := scopePool
	c := make([]*controlv1.Delegation, n)
	for i := range c {
		keep := rapid.SliceOfN(rapid.Bool(), len(held), len(held)).Draw(t, "kept")
		var granted []string
		for j, s := range held {
			if keep[j] {
				granted = append(granted, s)
			}
		}
		granted = rapid.Permutation(granted).Draw(t, "order")
		lifetime := time.Duration(rapid.Int64Range(1, int64(24*time.Hour)).Draw(t, "lifetime"))
		c[i] = &controlv1.Delegation{From: named[i], To: named[i+1], Scopes: granted, ExpiresAt: at(lifetime)}
		held = granted
	}
	return c
}

// drawAnyChain starts from a chain the rules allow and changes each hop with a
// probability that leaves about half the chains passing.
func drawAnyChain(t *rapid.T) []*controlv1.Delegation {
	c := drawNarrowingChain(t, 0)
	for i := range c {
		switch rapid.IntRange(0, 15).Draw(t, "change") {
		case 0:
			c[i] = nil
		case 1:
			c[i].ExpiresAt = drawExpiry(t)
		case 2:
			c[i].To = rapid.SampledFrom(partyPool).Draw(t, "to")
		case 3:
			c[i].From = rapid.SampledFrom(partyPool).Draw(t, "from")
		case 4:
			c[i].Scopes = append(c[i].Scopes, rapid.SampledFrom(scopePool).Draw(t, "added"))
		case 5:
			c[i].Scopes = rapid.SliceOfN(rapid.SampledFrom(scopePool), 0, 4).Draw(t, "scopes")
		}
	}
	return c
}

// deadExpiries are the expiries Check refuses: none, at the clock, before it,
// and two a Timestamp cannot hold, whose converted instants lie after it.
func deadExpiries() []*timestamppb.Timestamp {
	return []*timestamppb.Timestamp{
		nil,
		at(0),
		at(-time.Nanosecond),
		at(-time.Hour),
		{Seconds: clock().Unix() + 3600, Nanos: -1},
		{Seconds: lastSecondOf9999 + 1},
	}
}

func drawExpiry(t *rapid.T) *timestamppb.Timestamp {
	live := []*timestamppb.Timestamp{at(time.Nanosecond), at(time.Hour), {Seconds: lastSecondOf9999, Nanos: 999_999_999}}
	return rapid.SampledFrom(append(live, deadExpiries()...)).Draw(t, "expiry")
}

func expireOneHop(t *rapid.T, c []*controlv1.Delegation) {
	k := rapid.IntRange(0, len(c)-1).Draw(t, "expired hop")
	c[k].ExpiresAt = rapid.SampledFrom(deadExpiries()).Draw(t, "dead expiry")
}

// widenOneHop adds to a hop past the first a scope that no pool holds, so its
// parent cannot hold it. The hop after it still fits inside it.
func widenOneHop(t *rapid.T, c []*controlv1.Delegation) {
	k := rapid.IntRange(1, len(c)-1).Draw(t, "widened hop")
	c[k].Scopes = append(c[k].Scopes, "outside-every-pool")
}

// returnToAnEarlierParty points a hop back at a party the chain named before
// it, the hop's own sender included, and keeps the next hop linked to it.
func returnToAnEarlierParty(t *rapid.T, c []*controlv1.Delegation) {
	k := rapid.IntRange(0, len(c)-1).Draw(t, "returning hop")
	earlier := []string{c[0].GetFrom()}
	for _, h := range c[:k] {
		earlier = append(earlier, h.GetTo())
	}
	back := rapid.SampledFrom(earlier).Draw(t, "party met again")
	c[k].To = back
	if k+1 < len(c) {
		c[k+1].From = back
	}
}

// allowedByTheRules is ADR-0012's delegation rule read word for word, by
// brute force: every hop expires after now; no party is named twice, where a
// hop's sender is the same naming as the recipient before it when the two are
// equal; and every scope of a hop past the first is one its parent holds.
func allowedByTheRules(c []*controlv1.Delegation, now time.Time) bool {
	return everyHopExpiresAfter(c, now) && noPartyNamedTwice(c) && everyHopWithinItsParent(c)
}

func everyHopExpiresAfter(c []*controlv1.Delegation, now time.Time) bool {
	for _, h := range c {
		ts := h.GetExpiresAt()
		if ts.CheckValid() != nil || !ts.AsTime().After(now) {
			return false
		}
	}
	return true
}

func noPartyNamedTwice(c []*controlv1.Delegation) bool {
	var named []string
	for i, h := range c {
		if i == 0 || h.GetFrom() != c[i-1].GetTo() {
			named = append(named, h.GetFrom())
		}
		named = append(named, h.GetTo())
	}
	for i := range named {
		for j := range i {
			if named[i] == named[j] {
				return false
			}
		}
	}
	return true
}

func everyHopWithinItsParent(c []*controlv1.Delegation) bool {
	for i := 1; i < len(c); i++ {
		for _, s := range c[i].GetScopes() {
			if !slices.Contains(c[i-1].GetScopes(), s) {
				return false
			}
		}
	}
	return true
}
