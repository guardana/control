package policystate_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

func TestAFloorPastTheJSONSafeSerialIsNeverMade(t *testing.T) {
	dir, s := raised(t)
	past, err := policy.NewFloor(idA, 9007199254740992, d5, at(t, "10:00:00"), at(t, "10:00:00"))
	if !errors.Is(err, policy.ErrFloorInvalid) {
		t.Errorf("NewFloor at 2^53: %v, want ErrFloorInvalid", err)
	}
	if err == nil {
		before := readFile(t, filepath.Join(dir, fileA))
		if _, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, past, "past the bound"); !errors.Is(err, policystate.ErrUnwritable) {
			t.Errorf("Reset to a floor past 2^53-1: %v, want ErrUnwritable", err)
		}
		if after := readFile(t, filepath.Join(dir, fileA)); after != before {
			t.Errorf("Reset wrote a floor the reader refuses: %s", after)
		}
	}
	top, err := policy.NewFloor(idA, 9007199254740991, d5, at(t, "10:00:00"), at(t, "10:00:00"))
	if err != nil {
		t.Fatalf("NewFloor at 2^53-1: %v", err)
	}
	if _, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, top, "the last serial"); err != nil {
		t.Fatalf("Reset to 2^53-1: %v", err)
	}
	if f, err := s.Floor(t.Context(), idA); err != nil || f.Serial() != 9007199254740991 {
		t.Errorf("Floor after a reset to 2^53-1: %s, %v", describe(f), err)
	}
	if _, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), "back to 3"); err != nil {
		t.Errorf("a second Reset from 2^53-1: %v", err)
	}
}

// markerListing is a plane's marker listing bundle-a and n more ids, each
// sorted after it.
func markerListing(n int) string {
	ids := []string{`"bundle-a"`}
	for i := range n {
		ids = append(ids, fmt.Sprintf(`"id-%02d"`, i))
	}
	return `{"schema_version":"1.0","kind":"plane","bundle_ids":[` + strings.Join(ids, ",") + "]}\n"
}

func TestADirectoryListsAtMost32BundleIDs(t *testing.T) {
	dir := initDir(t, policystate.KindPlane)
	writeFile(t, filepath.Join(dir, "floors.meta"), markerListing(30))
	if err := policystate.Init(t.Context(), dir, policystate.KindPlane, "id-x1"); err != nil {
		t.Fatalf("Init of the 32nd id: %v", err)
	}
	if err := policystate.Init(t.Context(), dir, policystate.KindPlane, "id-x2"); !errors.Is(err, policystate.ErrTooManyBundleIDs) || errors.Is(err, policystate.ErrTooManyRouteIDs) || errors.Is(err, policystate.ErrUnwritable) {
		t.Errorf("Init of the 33rd id: %v, want ErrTooManyBundleIDs before any marker is encoded", err)
	}
	writeFile(t, filepath.Join(dir, "floors.meta"), markerListing(32))
	if _, err := policystate.Open(dir, policystate.KindPlane); !errors.Is(err, policystate.ErrNotStateDir) {
		t.Errorf("Open of a marker listing 33 ids: %v, want ErrNotStateDir", err)
	}
}

func TestTheMarkerIsReadUpToItsBound(t *testing.T) {
	for name, tc := range map[string]struct {
		size int
		want error
	}{
		"exactly the bound": {73728, nil},
		"one byte over":     {73729, policystate.ErrTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			dir := initDir(t, policystate.KindPlane)
			body := markerListing(0)
			writeFile(t, filepath.Join(dir, "floors.meta"), body+strings.Repeat(" ", tc.size-len(body)))
			s, err := policystate.Open(dir, policystate.KindPlane)
			if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("Open beside a marker of %d bytes: %v, want %v", tc.size, err, tc.want)
			}
			if err == nil {
				_ = s.Close()
			}
		})
	}
}
