//go:build unix

package stoplist

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/internal/reaction"
)

// TestAListThePlanesAccountOwnsIsRefusedUnderOwnedByAnother: the signed
// read refuses a directory or a file the reading account owns, with its own
// cause, takes a list another account owns, and AnyOwner takes either.
func TestAListThePlanesAccountOwnsIsRefusedUnderOwnedByAnother(t *testing.T) {
	me, other := os.Geteuid(), os.Geteuid()+1
	for _, c := range []struct {
		name  string
		owner Owner
		uids  []int
		err   error
	}{
		{"another account's directory and file", OwnedByAnother, []int{other}, nil},
		{"the plane's directory", OwnedByAnother, []int{me}, ErrPlaneOwned},
		{"the plane's file in another account's directory", OwnedByAnother, []int{other, me}, ErrPlaneOwned},
		{"the plane's own, any owner", AnyOwner, []int{me}, nil},
		{"another account's, any owner", AnyOwner, []int{other}, nil},
		{"another account's, read as its writer", OwnedByThisUser, []int{other}, ErrDirOwner},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := listDir(t, stoppingList(t))
			actAs(t, c.uids...)
			raw, err := ReadAs(dir, c.owner)
			switch {
			case c.err == nil && (err != nil || !bytes.Equal(raw, stoppingList(t))):
				t.Fatalf("ReadAs = %d bytes, %v; want the list", len(raw), err)
			case c.err != nil && !errors.Is(err, c.err):
				t.Fatalf("ReadAs = %v, want %v", err, c.err)
			}
			if errors.Is(c.err, ErrPlaneOwned) && CauseOf(err) != CausePlaneOwned {
				t.Fatalf("CauseOf = %q, want %q", CauseOf(err), CausePlaneOwned)
			}
		})
	}
}

// TestTakeHoldsTheHeaderOfTheFirstReadOnly: Take sees the list the poller
// opens on and not a list that only grew, since the judge holds every later
// read to that header; a refusal at the first read is a poller that never
// opens, naming the refusal's cause.
func TestTakeHoldsTheHeaderOfTheFirstReadOnly(t *testing.T) {
	first := stoppingList(t)
	dir := listDir(t, first)
	var took []string
	clock := &settable{now: clock0}
	p, err := Open(Options{Dir: dir, Route: testRoute(t), Interval: poll, Clock: clock.read, Owner: AnyOwner,
		Take: func(l reaction.List) error {
			took = append(took, l.Header().ListID)
			return nil
		}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	grown := append(bytes.Clone(first), lines(stopLine(t, "f-3", "run-2", clock0))...)
	writeList(t, dir, grown)
	expect(t, "grown", p.Poll(), reaction.Stopped, "", len(grown), [2]bool{true, true})
	if len(took) != 1 || took[0] != "list-1" {
		t.Fatalf("Take saw %v, want list-1 once", took)
	}
	refusal := fmt.Errorf("%w: serial 1, the floor's 2", ErrBelowFloor)
	_, err = Open(Options{Dir: listDir(t, first), Route: testRoute(t), Interval: poll, Clock: clock.read,
		Take: func(reaction.List) error { return refusal }})
	if !errors.Is(err, ErrNotServable) || !strings.Contains(err.Error(), string(CauseBelowFloor)) {
		t.Fatalf("Open over a list Take refuses: %v", err)
	}
}
