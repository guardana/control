package policystate_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policystate"
)

// resetTo3 is idA's floor file once Reset lowered serial 5, renewed at 11:00,
// to serial 3 issued at 09:00 and renewed at 09:30.
const resetTo3 = `{"schema_version":"1.0","bundle_id":"bundle-a","serial":3,"digest":"` + d3 +
	`","issued_at":"2026-09-11T09:00:00Z","latest_issued_at":"2026-09-11T09:30:00Z","reset_reason":"serial 5 withdrawn","reset_from":{"serial":5,"digest":"` + d5 +
	`","issued_at":"2026-09-11T11:00:00Z","latest_issued_at":"2026-09-11T11:00:00Z"}}` + "\n"

func TestAResetRetriedAfterItsWriteFailedKeepsThePriorValue(t *testing.T) {
	dir, _ := raised(t)
	restore := policystate.SetReplace(func(root *os.Root, name string, body []byte, perm fs.FileMode) error {
		return errors.Join(files.ReplaceIn(root, name, body, perm), errCrash)
	})
	_, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), "serial 5 withdrawn")
	restore()
	if !errors.Is(err, errCrash) {
		t.Fatalf("Reset through a write that died after its rename: %v", err)
	}
	prior, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), "serial 5 withdrawn")
	if err != nil || prior.Serial() != 5 {
		t.Errorf("the retried Reset: %s, %v; want serial 5 as the floor it replaced", describe(prior), err)
	}
	if got := readFile(t, filepath.Join(dir, fileA)); got != resetTo3 {
		t.Errorf("the floor file holds\n%s\nwant\n%s", got, resetTo3)
	}
}

func TestAResetToTheSameFloorForAnotherReasonRecordsThatFloorAsPrior(t *testing.T) {
	dir, s := raised(t)
	for _, reason := range []string{"serial 5 withdrawn", "confirmed by the authority"} {
		if _, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), reason); err != nil {
			t.Fatalf("Reset (%s): %v", reason, err)
		}
	}
	rec, err := s.Record(t.Context(), idA)
	if err != nil || rec.Reset == nil || rec.Reset.Reason != "confirmed by the authority" || rec.Reset.From.Serial() != 3 {
		t.Errorf("Record: %s, %+v, %v; want the second reason, from serial 3", describe(rec.Floor), rec.Reset, err)
	}
}

func TestInitRefusesAnIDWhoseFileWasRemoved(t *testing.T) {
	dir, _ := raised(t)
	if err := os.Remove(filepath.Join(dir, fileA)); err != nil {
		t.Fatal(err)
	}
	if err := policystate.Init(t.Context(), dir, policystate.KindPlane, idA); !errors.Is(err, policystate.ErrFloorRemoved) {
		t.Errorf("Init of an id whose file was removed: %v, want ErrFloorRemoved", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, fileA)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused Init made the file: %v", err)
	}
}

func TestResetGivesARemovedFileItsFloorAndRecordsItWasMissing(t *testing.T) {
	dir, s := raised(t)
	if err := os.Remove(filepath.Join(dir, fileA)); err != nil {
		t.Fatal(err)
	}
	prior, err := policystate.Reset(t.Context(), dir, policystate.KindPlane, floorAt3(t, "09:30:00"), "the file was lost")
	if err != nil || describe(prior) != "bundle-a with no serial" {
		t.Fatalf("Reset of a removed file: %s, %v", describe(prior), err)
	}
	want := `{"schema_version":"1.0","bundle_id":"bundle-a","serial":3,"digest":"` + d3 +
		`","issued_at":"2026-09-11T09:00:00Z","latest_issued_at":"2026-09-11T09:30:00Z","reset_reason":"the file was lost","reset_from":null}` + "\n"
	if got := readFile(t, filepath.Join(dir, fileA)); got != want {
		t.Errorf("the floor file holds\n%s\nwant\n%s", got, want)
	}
	rec, err := s.Record(t.Context(), idA)
	if err != nil || rec.Reset == nil || !rec.Reset.FileMissing || rec.Reset.From.HasSerial() || rec.Floor.Serial() != 3 {
		t.Errorf("Record: %s, %+v, %v; want serial 3, the file recorded as missing", describe(rec.Floor), rec.Reset, err)
	}
}

func TestAFloorFileOfAnIDNeverInitialisedIsNotRead(t *testing.T) {
	dir, s := raised(t)
	bodyB := `{"schema_version":"1.0","bundle_id":"bundle-b","serial":null,"digest":null,"issued_at":null,"latest_issued_at":null,"reset_reason":null,"reset_from":null}` + "\n"
	writeFile(t, filepath.Join(dir, fileB), bodyB)
	if f, err := s.Floor(t.Context(), idB); !errors.Is(err, policystate.ErrNoFloor) {
		t.Errorf("Floor of an id the marker does not list: %s, %v; want ErrNoFloor", describe(f), err)
	}
	if f, err := s.Raise(t.Context(), statement(t, idB, 1, d3, "11:00:00"), noon(t)); !errors.Is(err, policystate.ErrNoFloor) {
		t.Errorf("Raise of an id the marker does not list: %s, %v; want ErrNoFloor", describe(f), err)
	}
	if got := readFile(t, filepath.Join(dir, fileB)); got != bodyB {
		t.Errorf("the planted file became %s", got)
	}
}
