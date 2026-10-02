package policystate_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

func TestInitMakesTheDirectoryItsMarkerAndAFloorWithNoSerial(t *testing.T) {
	for kind, marker := range map[policystate.Kind]string{
		policystate.KindPlane:  `{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a"]}` + "\n",
		policystate.KindSigner: `{"schema_version":"1.0","kind":"signer","bundle_ids":["bundle-a"]}` + "\n",
	} {
		t.Run(string(kind), func(t *testing.T) {
			dir := newDir(t)
			if err := policystate.Init(t.Context(), dir, kind, idA); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if got := readFile(t, filepath.Join(dir, "floors.meta")); got != marker {
				t.Errorf("the marker holds %q, want %q", got, marker)
			}
			if got := readFile(t, filepath.Join(dir, fileA)); got != emptyBodyA {
				t.Errorf("the floor file holds %q, want %q", got, emptyBodyA)
			}
			s, err := policystate.Open(dir, kind)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = s.Close() }()
			f, err := s.Floor(t.Context(), idA)
			if err != nil || describe(f) != "bundle-a with no serial" {
				t.Errorf("Floor: %s, %v; want bundle-a with no serial", describe(f), err)
			}
		})
	}
}

func TestInitNeverReplacesAFloorFile(t *testing.T) {
	dir, s := raised(t)
	before := readFile(t, filepath.Join(dir, fileA))
	if err := policystate.Init(t.Context(), dir, policystate.KindPlane, idA); !errors.Is(err, policystate.ErrExists) {
		t.Fatalf("Init over a raised floor: %v, want ErrExists", err)
	}
	if after := readFile(t, filepath.Join(dir, fileA)); after != before {
		t.Errorf("Init changed the floor file:\n%s\nwas\n%s", after, before)
	}
	f, err := s.Floor(t.Context(), idA)
	if err != nil || f.Serial() != 5 {
		t.Errorf("Floor after the refused Init: %s, %v; want serial 5", describe(f), err)
	}
}

func TestInitGivesASecondBundleIDItsFileInTheSameDirectory(t *testing.T) {
	dir := initDir(t, policystate.KindPlane)
	if err := policystate.Init(t.Context(), dir, policystate.KindPlane, idB); err != nil {
		t.Fatalf("Init of a second id: %v", err)
	}
	f, err := open(t, dir).Floor(t.Context(), idB)
	if err != nil || describe(f) != "bundle-b with no serial" {
		t.Errorf("Floor(%s): %s, %v", idB, describe(f), err)
	}
}

func TestInitRefusesTheOtherKindsDirectory(t *testing.T) {
	dir := initDir(t, policystate.KindSigner)
	if err := policystate.Init(t.Context(), dir, policystate.KindPlane, idB); !errors.Is(err, policystate.ErrWrongKind) {
		t.Errorf("Init of a plane's id in a signer's directory: %v, want ErrWrongKind", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, fileB)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused Init wrote %s: %v", fileB, err)
	}
}

func TestInitTakesAnEmptyDirectoryAndRefusesOneHoldingSomethingElse(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		dir := newDir(t)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := policystate.Init(t.Context(), dir, policystate.KindPlane, idA); err != nil {
			t.Errorf("Init of an empty directory: %v", err)
		}
	})
	t.Run("a foreign file", func(t *testing.T) {
		dir := newDir(t)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "notes.txt"), "{}")
		if err := policystate.Init(t.Context(), dir, policystate.KindPlane, idA); !errors.Is(err, policystate.ErrNotStateDir) {
			t.Errorf("Init: %v, want ErrNotStateDir", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 {
			t.Errorf("the refused Init left %v, %v; want only notes.txt", entries, err)
		}
	})
}

func TestInitRefusesABadKindOrBundleIDAndCreatesNothing(t *testing.T) {
	for name, call := range map[string]func(dir string) (error, error){
		"a kind": func(dir string) (error, error) {
			return policystate.Init(t.Context(), dir, policystate.Kind("planes"), idA), policystate.ErrKind
		},
		"no bundle id": func(dir string) (error, error) {
			return policystate.Init(t.Context(), dir, policystate.KindPlane, ""), policy.ErrFloorInvalid
		},
		"a bundle id with white space at its end": func(dir string) (error, error) {
			return policystate.Init(t.Context(), dir, policystate.KindPlane, "bundle-a "), policy.ErrFloorInvalid
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			if got, want := call(dir); !errors.Is(got, want) {
				t.Errorf("Init: %v, want %v", got, want)
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the refused Init made the directory: %v", err)
			}
		})
	}
}

func TestResetRecordsItsReasonAndTheFloorItReplaced(t *testing.T) {
	dir, s := raised(t)
	prior, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), "serial 5 withdrawn")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := describe(prior); got != "bundle-a serial 5 "+d5+" issued 2026-09-11T11:00:00Z latest 2026-09-11T11:00:00Z" {
		t.Errorf("Reset returned %s as the floor it replaced", got)
	}
	want := `{"schema_version":"1.0","bundle_id":"bundle-a","serial":3,"digest":"` + d3 +
		`","issued_at":"2026-09-11T09:00:00Z","latest_issued_at":"2026-09-11T09:30:00Z","reset_reason":"serial 5 withdrawn","reset_from":{"serial":5,"digest":"` + d5 +
		`","issued_at":"2026-09-11T11:00:00Z","latest_issued_at":"2026-09-11T11:00:00Z"}}` + "\n"
	if got := readFile(t, filepath.Join(dir, fileA)); got != want {
		t.Errorf("the floor file holds\n%s\nwant\n%s", got, want)
	}
	rec, err := s.Record(t.Context(), idA)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rec.Reset == nil || rec.Reset.Reason != "serial 5 withdrawn" || rec.Reset.From.Serial() != 5 || describe(rec.Floor) != "bundle-a serial 3 "+d3+" issued 2026-09-11T09:00:00Z latest 2026-09-11T09:30:00Z" {
		t.Errorf("Record: %s, reset %+v", describe(rec.Floor), rec.Reset)
	}
}

func TestARaiseAfterAResetKeepsItsRecord(t *testing.T) {
	dir, s := raised(t)
	if _, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), "serial 5 withdrawn"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := s.Raise(t.Context(), statement(t, idA, 4, d6, "11:30:00"), noon(t)); err != nil {
		t.Fatalf("Raise past the reset floor: %v", err)
	}
	rec, err := s.Record(t.Context(), idA)
	if err != nil || rec.Reset == nil || rec.Reset.Reason != "serial 5 withdrawn" || rec.Floor.Serial() != 4 {
		t.Errorf("a raise after a reset: %s, %+v, %v; want serial 4 with the reset still recorded", describe(rec.Floor), rec.Reset, err)
	}
}

func TestResetToNoSerialRecordsTheSerialItRemoved(t *testing.T) {
	dir, s := raised(t)
	empty, err := policy.EmptyFloor(idA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, empty, "a new authority"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	rec, err := s.Record(t.Context(), idA)
	if err != nil || rec.Floor.HasSerial() || rec.Reset == nil || rec.Reset.From.Serial() != 5 {
		t.Errorf("Record: %s, %+v, %v; want no serial, reset from serial 5", describe(rec.Floor), rec.Reset, err)
	}
	if _, err := s.Raise(t.Context(), statement(t, idA, 2, d3, "11:30:00"), noon(t)); err != nil {
		t.Errorf("Raise to serial 2 after a reset to no serial: %v", err)
	}
}

func TestRecordHoldsNoResetUntilOne(t *testing.T) {
	_, s := raised(t)
	rec, err := s.Record(t.Context(), idA)
	if err != nil || rec.Reset != nil {
		t.Errorf("Record of a floor never reset: %+v, %v", rec.Reset, err)
	}
}

func TestResetRefusesAReasonItCannotRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		reason string
		want   error
	}{
		"empty":                     {"", policystate.ErrReason},
		"white space at its start":  {" withdrawn", policystate.ErrReason},
		"an escape sequence":        {"withdrawn\x1b[2J", policystate.ErrReason},
		"a new line":                {"withdrawn\nserial 9 installed", policystate.ErrReason},
		"one byte over the bound":   {strings.Repeat("r", policystate.MaxReasonBytes+1), policystate.ErrReason},
		"exactly the bound":         {strings.Repeat("r", 256), nil},
		"words with spaces between": {"serial 5 withdrawn", nil},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := raised(t)
			before := readFile(t, filepath.Join(dir, fileA))
			_, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:00:00"), tc.reason)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("Reset: %v, want %v", err, tc.want)
			}
			if after := readFile(t, filepath.Join(dir, fileA)); tc.want != nil && after != before {
				t.Errorf("a refused Reset changed the file to %s", after)
			}
		})
	}
}

func TestResetRefusesWhatItHasNoFloorFor(t *testing.T) {
	floorB, err := policy.EmptyFloor(idB)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		kind policystate.Kind
		to   policy.Floor
		want error
	}{
		"an id with no file":   {policystate.KindPlane, floorB, policystate.ErrNoFloor},
		"the other kind":       {policystate.KindSigner, floorAt3(t, "09:00:00"), policystate.ErrWrongKind},
		"the zero Floor":       {policystate.KindPlane, policy.Floor{}, policy.ErrFloorInvalid},
		"a kind of no meaning": {policystate.Kind(""), floorAt3(t, "09:00:00"), policystate.ErrKind},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := raised(t)
			before := readFile(t, filepath.Join(dir, fileA))
			if _, err := policystate.Reset(t.Context(), dir, tc.kind, tc.to, "withdrawn"); !errors.Is(err, tc.want) {
				t.Errorf("Reset: %v, want %v", err, tc.want)
			}
			if after := readFile(t, filepath.Join(dir, fileA)); after != before {
				t.Errorf("a refused Reset changed the file to %s", after)
			}
			if _, err := os.Lstat(filepath.Join(dir, fileB)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused Reset made %s: %v", fileB, err)
			}
		})
	}
}
