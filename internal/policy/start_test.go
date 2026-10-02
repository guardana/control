package policy_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy"
)

// TestStartRule is ADR-0038's start, one row per case, judged at 13:00:00
// against the floor each row names. The bundles are loaded for real; their
// serials and digests are the test's own.
func TestStartRule(t *testing.T) {
	t.Parallel()
	own := load(t, valid().build()) // payments, serial 7, d7
	lower := load(t, signedDocument("payments", "v6", 6))
	beside := load(t, signedDocument("payments", "v7-other", 7))
	higher := load(t, signedDocument("payments", "v8", 8))
	other := load(t, signedDocument("risk", "v7", 7))
	d8real := higher.Ref().GetDigest()

	floor := floorOf(t, 7, d7, "12:00:00", "12:00:00")
	behind := floorOf(t, 7, d7, "11:00:00", "13:30:00")
	empty := emptyFloor(t, "payments")
	stOwn := statementFor(t, "payments", 7, d7, "12:00:00")
	stRenewal := statementFor(t, "payments", 7, d7, "12:30:00")
	stOlder := statementFor(t, "payments", 7, d7, "11:00:00")
	stFuture := statementFor(t, "payments", 7, d7, "13:00:01")
	stHigher := statementFor(t, "payments", 8, d8real, "12:30:00")
	stHigherFuture := statementFor(t, "payments", 8, d8real, "13:00:01")
	stHigherEarly := statementFor(t, "payments", 8, d8real, "11:30:00")
	stOwnEarly := statementFor(t, "payments", 7, d7, "11:00:00")

	cases := []startCase{
		{"no serial yet, no statement", empty, own, nil, policy.StartUnconfirmed, []error{policy.ErrStatementMissing}, 7, 0},
		{"no serial yet, its statement", empty, own, &stOwn, policy.StartConfirmed, nil, 7, 0},
		{"no serial yet, a statement dated ahead", empty, own, &stFuture, policy.StartUnconfirmed, []error{policy.ErrStatementFuture}, 7, 0},
		{"no serial yet, a statement for another serial", empty, own, &stHigher, policy.StartUnconfirmed, []error{policy.ErrStatementUnbound}, 7, 0},
		{"no serial yet, a bundle of another id", empty, other, nil, policy.StartRefused, []error{policy.ErrFloorBundle}, 7, 0},
		{"a lower bundle", floor, lower, nil, policy.StartRefused, []error{policy.ErrBelowFloor}, 6, 7},
		{"a lower bundle with a statement", floor, lower, &stOwn, policy.StartRefused, []error{policy.ErrBelowFloor}, 6, 7},
		{"the floor's serial, another digest", floor, beside, nil, policy.StartRefused, []error{policy.ErrFloorSerialReused}, 7, 7},
		{"the floor's own bundle, no statement", floor, own, nil, policy.StartUnconfirmed, []error{policy.ErrStatementMissing}, 7, 7},
		{"the floor's own bundle, its statement", floor, own, &stOwn, policy.StartConfirmed, nil, 7, 7},
		{"the floor's own bundle, a renewal", floor, own, &stRenewal, policy.StartConfirmed, nil, 7, 7},
		{"the floor's own bundle, an older statement", floor, own, &stOlder, policy.StartUnconfirmed, []error{policy.ErrBelowFloor}, 7, 7},
		{"the floor's own bundle, the higher bundle's statement", floor, own, &stHigher, policy.StartUnconfirmed, []error{policy.ErrStatementUnbound}, 7, 7},
		{"a higher bundle, no statement", floor, higher, nil, policy.StartRefused, []error{policy.ErrAboveFloorUnbound, policy.ErrStatementMissing}, 8, 7},
		{"a higher bundle, the floor's statement", floor, higher, &stOwn, policy.StartRefused, []error{policy.ErrAboveFloorUnbound, policy.ErrStatementUnbound}, 8, 7},
		{"a higher bundle, its statement", floor, higher, &stHigher, policy.StartConfirmed, nil, 8, 7},
		{"a higher bundle, its statement issued before the floor's", floor, higher, &stHigherEarly, policy.StartConfirmed, nil, 8, 7},
		{"a higher bundle, its statement dated ahead", floor, higher, &stHigherFuture, policy.StartRefused, []error{policy.ErrAboveFloorUnbound, policy.ErrStatementFuture}, 8, 7},
		{"a clock behind the latest, the floor's own bundle, its statement", behind, own, &stOwnEarly, policy.StartUnconfirmed, []error{policy.ErrClockBehindFloor}, 7, 7},
		{"a clock behind the latest, the floor's own bundle, no statement", behind, own, nil, policy.StartUnconfirmed, []error{policy.ErrClockBehindFloor}, 7, 7},
		{"a clock behind the latest, a higher bundle, its statement", behind, higher, &stHigherEarly, policy.StartRefused, []error{policy.ErrAboveFloorUnbound, policy.ErrClockBehindFloor}, 8, 7},
		{"a clock behind the latest, a higher bundle, no statement", behind, higher, nil, policy.StartRefused, []error{policy.ErrAboveFloorUnbound, policy.ErrClockBehindFloor}, 8, 7},
		{"the floor's own bundle, its statement dated ahead", floor, own, &stFuture, policy.StartUnconfirmed, []error{policy.ErrStatementFuture}, 7, 7},
		{"a bundle of another id", floor, other, nil, policy.StartRefused, []error{policy.ErrFloorBundle}, 7, 7},
		{"the zero Floor", policy.Floor{}, own, &stOwn, policy.StartRefused, []error{policy.ErrFloorInvalid}, 7, 0},
	}
	for _, c := range cases {
		checkStart(t, c, policy.StartRule(c.floor, c.snap, c.st, utc("13:00:00")))
	}
}

type startCase struct {
	name   string
	floor  policy.Floor
	snap   *policy.Snapshot
	st     *policy.Statement
	action policy.StartAction
	causes []error
	bundle int64
	floorS int64
}

// checkStart fails unless v is c's verdict: its action and serials, every
// cause c names, and on a refusal against a floor with a serial, both serials
// in the cause's text.
func checkStart(t *testing.T, c startCase, v policy.StartVerdict) {
	t.Helper()
	if v.Action != c.action || v.BundleSerial != c.bundle || v.FloorSerial != c.floorS {
		t.Errorf("%s: action %d, serials %d and %d; want %d, %d and %d", c.name, v.Action, v.BundleSerial, v.FloorSerial, c.action, c.bundle, c.floorS)
	}
	if c.causes == nil {
		if v.Cause != nil {
			t.Errorf("%s: confirmed with a cause: %v", c.name, v.Cause)
		}
		return
	}
	for _, want := range c.causes {
		if !errors.Is(v.Cause, want) {
			t.Errorf("%s: cause %v, want it to be %q", c.name, v.Cause, want)
		}
	}
	if c.action == policy.StartRefused && c.floorS != 0 {
		expectBothSerials(t, c, v.Cause)
	}
}

func expectBothSerials(t *testing.T, c startCase, cause error) {
	t.Helper()
	for _, serial := range []string{"bundle serial " + strconv.FormatInt(c.bundle, 10), "floor serial " + strconv.FormatInt(c.floorS, 10)} {
		if cause == nil || !strings.Contains(cause.Error(), serial) {
			t.Errorf("%s: the refusal %v does not name %q", c.name, cause, serial)
		}
	}
}

// A start with no bundle, and the zero verdict, refuse.
func TestStartRuleRefusesNoBundle(t *testing.T) {
	t.Parallel()
	v := policy.StartRule(floorOf(t, 7, d7, "12:00:00", "12:00:00"), nil, nil, utc("13:00:00"))
	if v.Action != policy.StartRefused || !errors.Is(v.Cause, policy.ErrNoBundle) {
		t.Fatalf("no bundle: %d, %v", v.Action, v.Cause)
	}
	if (policy.StartVerdict{}).Action != policy.StartRefused {
		t.Fatal("the zero verdict does not refuse")
	}
}
