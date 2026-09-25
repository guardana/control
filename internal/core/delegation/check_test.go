// Check against the delegation rules of ADR-0012, from outside the package:
// these tests see what the kernel sees.
package delegation_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core/delegation"
)

// The three codes, written out rather than read from the package, so that a
// test cannot agree with a code the package spells wrong.
const (
	expired       = "DELEGATION_EXPIRED"
	cycle         = "DELEGATION_CYCLE"
	exceedsParent = "DELEGATION_EXCEEDS_PARENT"
)

// lastSecondOf9999 is 9999-12-31T23:59:59Z, the last second a Timestamp can
// hold.
const lastSecondOf9999 = 253402300799

// clock is the receiver's clock in every test. Its nanosecond is not zero, so
// an expiry one nanosecond either side of it is another instant, not a
// rounding of the same one.
func clock() time.Time { return time.Date(2026, 9, 11, 12, 0, 0, 500, time.UTC) }

// at is an expiry d after the clock.
func at(d time.Duration) *timestamppb.Timestamp { return timestamppb.New(clock().Add(d)) }

func hop(from, to string, expires *timestamppb.Timestamp, scopes ...string) *controlv1.Delegation {
	return &controlv1.Delegation{From: from, To: to, Scopes: scopes, ExpiresAt: expires}
}

func chain(hops ...*controlv1.Delegation) []*controlv1.Delegation { return hops }

// TestCheck holds each rule to the input at which removing it changes the
// answer, beside a twin that shows the rule is not a refusal of everything.
func TestCheck(t *testing.T) {
	later := at(time.Hour)
	cases := []struct {
		name   string
		chain  []*controlv1.Delegation
		code   string   // the refusal, "" for a chain that passes
		scopes []string // what a chain that passes grants
	}{
		// A hop's scopes are a subset of its parent's.
		{name: "equal scope sets pass", scopes: []string{"b", "a"},
			chain: chain(hop("alice", "planner", later, "a", "b"), hop("planner", "worker", later, "b", "a"))},
		{name: "one extra scope refuses", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a", "b"))},
		{name: "a narrower set passes", scopes: []string{"b"},
			chain: chain(hop("alice", "planner", later, "a", "b"), hop("planner", "worker", later, "b"))},
		{name: "an empty parent refuses a child scope", code: exceedsParent,
			chain: chain(hop("alice", "planner", later), hop("planner", "worker", later, "a"))},
		{name: "an empty parent passes an empty child", scopes: nil,
			chain: chain(hop("alice", "planner", later), hop("planner", "worker", later))},
		{name: "a hop is held to its parent, not to the root", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "a", "b"), hop("planner", "worker", later, "a"),
				hop("worker", "helper", later, "a", "b"))},
		{name: "a middle hop beyond its parent refuses, though the last hop is within its own", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a", "b"),
				hop("worker", "helper", later, "a"))},
		{name: "the first hop is held to nothing in the chain", scopes: []string{"a", "b", "c"},
			chain: chain(hop("alice", "worker", later, "a", "b", "c"))},
		{name: "a repeated scope is one scope", scopes: []string{"a", "a"},
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a", "a"))},
		{name: "scopes compare exactly, case included", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "tools:read"), hop("planner", "worker", later, "Tools:read"))},
		{name: "scopes compare exactly, white space included", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a "))},
		{name: "a scope covers no other by prefix", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "tools"), hop("planner", "worker", later, "tools:read"))},
		{name: "the empty scope is a scope like any other", code: exceedsParent,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a", ""))},

		// Every hop expires after the clock.
		{name: "no expiry refuses", code: expired,
			chain: chain(hop("alice", "worker", nil, "a"))},
		{name: "no expiry on the last hop refuses", code: expired,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", nil, "a"))},
		{name: "an expiry exactly at now refuses", code: expired,
			chain: chain(hop("alice", "worker", at(0), "a"))},
		{name: "an expiry a nanosecond before now refuses", code: expired,
			chain: chain(hop("alice", "worker", at(-time.Nanosecond), "a"))},
		{name: "an expiry a nanosecond after now passes", scopes: []string{"a"},
			chain: chain(hop("alice", "worker", at(time.Nanosecond), "a"))},
		{name: "an expired hop between two live ones refuses", code: expired,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", at(0), "a"),
				hop("worker", "helper", later, "a"))},
		{name: "nanos a timestamp cannot hold refuse", code: expired,
			chain: chain(hop("alice", "worker", &timestamppb.Timestamp{Seconds: later.GetSeconds(), Nanos: 1_000_000_000}, "a"))},
		{name: "an expiry past the year 9999 refuses", code: expired,
			chain: chain(hop("alice", "worker", &timestamppb.Timestamp{Seconds: lastSecondOf9999 + 1}, "a"))},
		{name: "the last instant of the year 9999 passes", scopes: []string{"a"},
			chain: chain(hop("alice", "worker", &timestamppb.Timestamp{Seconds: lastSecondOf9999, Nanos: 999_999_999}, "a"))},
		{name: "a nil hop refuses", code: expired,
			chain: []*controlv1.Delegation{nil}},

		// No party is met twice.
		{name: "a party delegating to itself refuses", code: cycle,
			chain: chain(hop("alice", "alice", later, "a"))},
		{name: "a chain back to its root refuses", code: cycle,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "alice", later, "a"))},
		{name: "a cycle of three refuses", code: cycle,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a"),
				hop("worker", "alice", later, "a"))},
		{name: "a party met again further down refuses", code: cycle,
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a"),
				hop("worker", "planner", later, "a"))},
		{name: "a chain that meets each party once passes", scopes: []string{"a"},
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "worker", later, "a"),
				hop("worker", "helper", later, "a"))},
		{name: "parties compare exactly, case included", scopes: []string{"a"},
			chain: chain(hop("alice", "planner", later, "a"), hop("planner", "Planner", later, "a"))},
		// Validate refuses a chain whose hops do not link, and Check may be
		// handed one that never met Validate: a party is still met twice.
		{name: "a hop that does not continue the one before still names its party", code: cycle,
			chain: chain(hop("alice", "planner", later, "a"), hop("alice", "worker", later, "a"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := delegation.Check(c.chain, clock())
			if c.code == "" {
				assertGrants(t, got, err, c.scopes)
				return
			}
			assertRefuses(t, got, err, c.code)
		})
	}
}

// TestCheckNamesOneFaultInAFixedOrder: a chain wrong in more than one way gets
// one code, and which one does not depend on where each fault sits: an expired
// hop first, then a party met twice, then a hop beyond its parent. Each case
// puts the less urgent fault nearer the root, so a walk that stops at the
// first bad hop names the other code.
func TestCheckNamesOneFaultInAFixedOrder(t *testing.T) {
	later := at(time.Hour)
	cases := []struct {
		name  string
		chain []*controlv1.Delegation
		code  string
	}{
		{"beyond its parent at hop 1, expired at hop 2", chain(hop("alice", "planner", later, "a"),
			hop("planner", "worker", later, "a", "b"), hop("worker", "helper", at(0), "a")), expired},
		{"back to the root at hop 1, expired at hop 2", chain(hop("alice", "planner", later, "a"),
			hop("planner", "alice", later, "a"), hop("alice", "helper", at(0), "a")), expired},
		{"beyond its parent at hop 1, back to the root at hop 2", chain(hop("alice", "planner", later, "a"),
			hop("planner", "worker", later, "a", "b"), hop("worker", "alice", later, "a", "b")), cycle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := delegation.Check(c.chain, clock())
			assertRefuses(t, got, err, c.code)
		})
	}
}

func TestNoChainDelegatesNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		chain []*controlv1.Delegation
	}{{"nil", nil}, {"empty", []*controlv1.Delegation{}}} {
		t.Run(c.name, func(t *testing.T) {
			got, err := delegation.Check(c.chain, clock())
			if err != nil {
				t.Fatalf("Check refused no chain: %v", err)
			}
			if got.Delegated || got.Scopes != nil {
				t.Fatalf("no chain gave %+v, want Delegated false and no scopes", got)
			}
		})
	}
}

// TestCheckHandsOutItsOwnScopes: the chain belongs to the caller and the
// result to whoever evaluates it, and neither may reach the other.
func TestCheckHandsOutItsOwnScopes(t *testing.T) {
	later := at(time.Hour)
	c := chain(hop("alice", "planner", later, "a", "b"), hop("planner", "worker", later, "a", "b"))
	got, err := delegation.Check(c, clock())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got.Scopes[0] = "written to the result"
	if c[1].Scopes[0] != "a" {
		t.Errorf("writing to the result changed the chain: %q", c[1].Scopes)
	}
	c[1].Scopes[1] = "written to the chain"
	if got.Scopes[1] != "b" {
		t.Errorf("writing to the chain changed the result: %q", got.Scopes)
	}
}

// TestCheckLeavesTheChainAsItFoundIt: a subset check that sorted the scopes in
// place would reorder the caller's envelope.
func TestCheckLeavesTheChainAsItFoundIt(t *testing.T) {
	later := at(time.Hour)
	for _, c := range []struct {
		name   string
		chain  []*controlv1.Delegation
		passes bool
	}{
		{"passing", chain(hop("alice", "planner", later, "b", "a"), hop("planner", "worker", later, "a")), true},
		{"refused", chain(hop("alice", "planner", later, "b"), hop("planner", "worker", later, "b", "a")), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := make([]*controlv1.Delegation, len(c.chain))
			for i, h := range c.chain {
				before[i] = proto.CloneOf(h)
			}
			if _, err := delegation.Check(c.chain, clock()); (err == nil) != c.passes {
				t.Fatalf("Check: err = %v, want passes = %v", err, c.passes)
			}
			for i := range c.chain {
				if !proto.Equal(before[i], c.chain[i]) {
					t.Errorf("hop %d changed: %v, was %v", i, c.chain[i], before[i])
				}
			}
		})
	}
}

func assertGrants(t *testing.T, got delegation.Effective, err error, scopes []string) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check refused a chain the rules allow: %v", err)
	}
	if !got.Delegated {
		t.Fatal("Delegated = false for a chain that passed")
	}
	if !slices.Equal(got.Scopes, scopes) {
		t.Fatalf("Scopes = %q, want %q", got.Scopes, scopes)
	}
}

func assertRefuses(t *testing.T, got delegation.Effective, err error, code string) {
	t.Helper()
	var refusal *delegation.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a *delegation.Error with %s", err, code)
	}
	if refusal.Code != code {
		t.Fatalf("Code = %s, want %s", refusal.Code, code)
	}
	// The message is the code and nothing a producer wrote.
	if want := "delegation: " + code; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	// A refused chain grants nothing, so a caller that drops the error holds
	// no scopes either.
	if got.Delegated || got.Scopes != nil {
		t.Errorf("a refusal returned %+v, want the zero Effective", got)
	}
}
