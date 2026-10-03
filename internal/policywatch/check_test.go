package policywatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policywatch"
)

// TestCheckRefusesWhatAStartWouldNotConfirm: each case is refused naming the
// input that refused it, the statement or the bundle, and none writes the floor; the files a start would
// confirm pass, and leave the floor where it was too.
func TestCheckRefusesWhatAStartWouldNotConfirm(t *testing.T) {
	floorDigest := signed(t, doc(planeID, 2, "v2", 600), bundleKey).GetRef().GetDigest()
	for _, c := range []struct {
		name  string
		floor func(t *testing.T) policy.Floor
		files func(r *rig)
		wants error
		says  string
		input string
	}{
		{"no statement", func(t *testing.T) policy.Floor { return emptyFloor(t) },
			func(r *rig) { r.writeBundle(2, "v2", 600) }, policy.ErrStatementMissing, "", "statement"},
		{"a statement that does not verify", func(t *testing.T) policy.Floor { return emptyFloor(t) }, func(r *rig) {
			r.write(r.opts.StatementPath, statementBytes(r.t, planeID, 2, r.writeBundle(2, "v2", 600), t0, otherKey, freshKeyID))
		}, policy.ErrStatementSignature, "", "statement"},
		{"an expired statement", func(t *testing.T) policy.Floor { return emptyFloor(t) }, func(r *rig) {
			r.writeStatement(2, r.writeBundle(2, "v2", 600), r.clock.Wall().Add(-11*time.Minute))
		}, nil, "expired at", "statement"},
		{"a statement dated ahead", func(t *testing.T) policy.Floor { return emptyFloor(t) }, func(r *rig) {
			r.writeStatement(2, r.writeBundle(2, "v2", 600), r.clock.Wall().Add(time.Second))
		}, policy.ErrStatementFuture, "", "statement"},
		{"a statement bound to another bundle", func(t *testing.T) policy.Floor { return emptyFloor(t) }, func(r *rig) {
			r.writeBundle(2, "v2", 600)
			r.writeStatement(2, floorDigest[:len(floorDigest)-1]+"0", t0)
		}, policy.ErrStatementUnbound, "", "statement"},
		{"a bundle below the floor", func(t *testing.T) policy.Floor { return floorAt2(t, floorDigest, t0) }, func(r *rig) {
			r.writeStatement(1, r.writeBundle(1, "v1", 600), t0.Add(time.Second))
		}, policy.ErrBelowFloor, "bundle serial 1, floor serial 2", "bundle"},
		{"a bundle beside the floor", func(t *testing.T) policy.Floor { return floorAt2(t, floorDigest, t0) }, func(r *rig) {
			r.writeStatement(2, r.writeBundle(2, "v2-other", 600), t0.Add(time.Second))
		}, policy.ErrFloorSerialReused, "bundle serial 2, floor serial 2", "bundle"},
		{"a statement below the floor", func(t *testing.T) policy.Floor { return floorAt2(t, floorDigest, t0) }, func(r *rig) {
			r.writeStatement(2, r.writeBundle(2, "v2", 600), t0.Add(-time.Second))
		}, policy.ErrBelowFloor, "", "statement"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, c.floor(t))
			before, _ := r.store.stored()
			c.files(r)
			_, err := policywatch.Check(context.Background(), r.opts)
			var se *policywatch.StartError
			switch {
			case err == nil:
				t.Fatal("Check passed it")
			case !errors.As(err, &se) || se.Input != c.input:
				t.Errorf("Check = %v, want a refusal of the %s", err, c.input)
			case c.wants != nil && !errors.Is(err, c.wants):
				t.Errorf("Check = %v, want %v", err, c.wants)
			case c.says != "" && !strings.Contains(err.Error(), c.says):
				t.Errorf("Check = %v, want it to say %q", err, c.says)
			}
			if after, writes := r.store.stored(); writes != 0 || !after.Equal(before) {
				t.Errorf("Check moved the floor to %v with %d write(s)", after, writes)
			}
		})
	}
}

func TestCheckPassesWhatAStartWouldConfirmAndWritesNothing(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	digest := r.writeBundle(2, "v2", 600)
	r.writeStatement(2, digest, t0)
	rep, err := policywatch.Check(context.Background(), r.opts)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	if rep.Bundle.Serial() != 2 || rep.Statement.Digest() != digest || !rep.Expires.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("Check reports serial %d, statement %s, expiry %v", rep.Bundle.Serial(), rep.Statement.Digest(), rep.Expires)
	}
	if f, writes := r.store.stored(); f.HasSerial() || writes != 0 {
		t.Errorf("Check raised the floor to %v", f)
	}
	if r.holder.Current() != nil {
		t.Error("Check installed a bundle")
	}
}
