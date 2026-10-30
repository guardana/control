package policystate_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policystate"
)

// The file names of the roster ids these tests use: the hex SHA-256 of the
// authority name domain and the id, computed apart from the code under test.
const (
	authorityFileA  = "aa9cf7aaa04327b2aa05802ffb6b7c66b3c037b162ff5640229b8cf7cec71d5b.authority.json"
	authorityFileB  = "20973fd337f590f368a2754b8816567e4144a5ca154d993e7ece60d6db9805b4.authority.json"
	authorityMarker = "authority.meta"
)

const (
	rosterA = "roster-a"
	rosterB = "roster-b"
)

const (
	authorityMarkerA = `{"schema_version":"1.0","kind":"authority","roster_ids":["roster-a"]}` + "\n"
	authorityEmptyA  = `{"schema_version":"1.0","roster_id":"roster-a","serial":null,"digest":null}` + "\n"
	authorityAt5     = `{"schema_version":"1.0","roster_id":"roster-a","serial":5,"digest":"` + d5 + `"}` + "\n"
)

func authorityDir(t testing.TB) string {
	t.Helper()
	dir := newDir(t)
	if err := policystate.InitAuthority(t.Context(), dir, rosterA); err != nil {
		t.Fatalf("InitAuthority(%s): %v", dir, err)
	}
	return dir
}

func authorityAt5Dir(t testing.TB) string {
	t.Helper()
	dir := authorityDir(t)
	if _, err := policystate.RaiseAuthority(t.Context(), dir, rosterA, 5, d5); err != nil {
		t.Fatalf("RaiseAuthority to 5: %v", err)
	}
	return dir
}

func TestInitAuthorityMakesAFloorWithNoSerial(t *testing.T) {
	dir := authorityDir(t)
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory: %v, %v; want mode 0700", info, err)
	}
	if got := readFile(t, filepath.Join(dir, authorityMarker)); got != authorityMarkerA {
		t.Errorf("the marker = %q, want %q", got, authorityMarkerA)
	}
	if got := readFile(t, filepath.Join(dir, authorityFileA)); got != authorityEmptyA {
		t.Errorf("the floor file = %q, want %q", got, authorityEmptyA)
	}
	f, err := policystate.ReadAuthority(dir, rosterA)
	if err != nil || f.HasSerial() || f.RosterID != rosterA {
		t.Errorf("ReadAuthority = %+v, %v; want roster-a with no serial", f, err)
	}
}

func TestInitAuthorityListsASecondRosterAndNeverMakesAFileAgain(t *testing.T) {
	dir := authorityDir(t)
	if err := policystate.InitAuthority(t.Context(), dir, rosterB); err != nil {
		t.Fatalf("InitAuthority of a second roster: %v", err)
	}
	want := `{"schema_version":"1.0","kind":"authority","roster_ids":["roster-a","roster-b"]}` + "\n"
	if got := readFile(t, filepath.Join(dir, authorityMarker)); got != want {
		t.Errorf("the marker = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, authorityFileB)); err != nil {
		t.Errorf("roster-b's file: %v", err)
	}
	if err := policystate.InitAuthority(t.Context(), dir, rosterA); !errors.Is(err, policystate.ErrExists) {
		t.Errorf("InitAuthority of a listed id = %v, want ErrExists", err)
	}
	if err := os.Remove(filepath.Join(dir, authorityFileA)); err != nil {
		t.Fatal(err)
	}
	if err := policystate.InitAuthority(t.Context(), dir, rosterA); !errors.Is(err, policystate.ErrFloorRemoved) {
		t.Errorf("InitAuthority of an id whose file was removed = %v, want ErrFloorRemoved", err)
	}
	if _, err := policystate.RaiseAuthority(t.Context(), dir, rosterA, 1, d5); !errors.Is(err, policystate.ErrNoFloor) {
		t.Errorf("RaiseAuthority of an id whose file was removed = %v, want ErrNoFloor", err)
	}
}

func TestRaiseAuthorityTakesOnlyAHigherSerialOrTheSameRoster(t *testing.T) {
	dir := authorityAt5Dir(t)
	if got := readFile(t, filepath.Join(dir, authorityFileA)); got != authorityAt5 {
		t.Fatalf("after the raise the file = %q, want %q", got, authorityAt5)
	}
	agePast(t, dir)
	before := snapshot(t, dir)
	if f, err := policystate.RaiseAuthority(t.Context(), dir, rosterA, 5, d5); err != nil || f.Serial != 5 || f.Digest != d5 {
		t.Errorf("the same roster again = %+v, %v; want serial 5 kept", f, err)
	}
	sameSnapshot(t, "taking the floor's own roster", before, snapshot(t, dir))
	refusals := []struct {
		name   string
		serial int64
		digest string
		want   error
	}{
		{"a lower serial", 4, d3, policystate.ErrAuthorityBelowFloor},
		{"the floor's serial with another digest", 5, d6, policystate.ErrAuthorityForked},
		{"serial 0", 0, d5, policystate.ErrAuthorityInvalid},
		{"a serial past the JSON-safe range", 9007199254740992, d5, policystate.ErrAuthorityInvalid},
		{"a digest no roster has", 6, "sha256:5", policystate.ErrAuthorityInvalid},
	}
	for _, c := range refusals {
		_, err := policystate.RaiseAuthority(t.Context(), dir, rosterA, c.serial, c.digest)
		wantRefusedUnchanged(t, c.name, err, c.want, before, snapshot(t, dir))
	}
	if f, err := policystate.RaiseAuthority(t.Context(), dir, rosterA, 6, d6); err != nil || f.Serial != 6 || f.Digest != d6 {
		t.Errorf("serial 6 = %+v, %v; want it taken", f, err)
	}
	if f, err := policystate.ReadAuthority(dir, rosterA); err != nil || f.Serial != 6 || f.Digest != d6 {
		t.Errorf("ReadAuthority after serial 6 = %+v, %v", f, err)
	}
}

func wantRefusedUnchanged(t *testing.T, what string, err, want error, before, after map[string]string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: RaiseAuthority = %v, want %v", what, err, want)
	}
	sameSnapshot(t, what, before, after)
}

func TestAnAuthorityFloorIsNoOtherKindsDirectory(t *testing.T) {
	route := routeDir(t)
	plane := initDir(t, policystate.KindPlane)
	for name, dir := range map[string]string{"a route floor directory": route, "a plane's directory": plane} {
		if _, err := policystate.ReadAuthority(dir, rosterA); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("ReadAuthority of %s = %v, want ErrWrongKind", name, err)
		}
		if _, err := policystate.RaiseAuthority(t.Context(), dir, rosterA, 1, d5); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("RaiseAuthority of %s = %v, want ErrWrongKind", name, err)
		}
		if err := policystate.InitAuthority(t.Context(), dir, rosterA); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("InitAuthority of %s = %v, want ErrWrongKind", name, err)
		}
	}
	dir := authorityDir(t)
	if _, err := policystate.ReadRoute(dir, rosterA); err == nil {
		t.Error("ReadRoute read an authority floor directory")
	}
	if _, err := policystate.Open(dir, policystate.KindPlane); err == nil {
		t.Error("Open read an authority floor directory as a plane's")
	}
	if err := policystate.InitRoute(t.Context(), dir, rosterA); err == nil {
		t.Error("InitRoute made a route floor in an authority floor directory")
	}
}

func TestAnAuthorityDirectoryHoldingAForeignNameIsRefused(t *testing.T) {
	dir := authorityAt5Dir(t)
	writeFile(t, filepath.Join(dir, authorityFileB), strings.Replace(authorityAt5, rosterA, rosterB, 1))
	if _, err := policystate.ReadAuthority(dir, rosterA); !errors.Is(err, policystate.ErrForeignFile) {
		t.Errorf("a floor file of an unlisted roster = %v, want ErrForeignFile", err)
	}
}

func TestAnAuthorityFloorFileNamingAnotherRosterIsRefused(t *testing.T) {
	dir := authorityDir(t)
	if err := policystate.InitAuthority(t.Context(), dir, rosterB); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, authorityFileA), strings.Replace(authorityAt5, rosterA, rosterB, 1))
	if _, err := policystate.ReadAuthority(dir, rosterA); !errors.Is(err, policystate.ErrNameMismatch) {
		t.Errorf("roster-a's file naming roster-b = %v, want ErrNameMismatch", err)
	}
}

func TestInitAuthorityStopsAtMaxRosterIDs(t *testing.T) {
	dir := newDir(t)
	for i := range policystate.MaxRosterIDs {
		id := "roster-" + strings.Repeat("x", i+1)
		if err := policystate.InitAuthority(t.Context(), dir, id); err != nil {
			t.Fatalf("InitAuthority of id %d: %v", i+1, err)
		}
	}
	if err := policystate.InitAuthority(t.Context(), dir, "roster-one-more"); !errors.Is(err, policystate.ErrTooManyRosterIDs) {
		t.Errorf("InitAuthority past MaxRosterIDs = %v, want ErrTooManyRosterIDs", err)
	}
}

func TestAnAuthorityRosterIDIsHeldToTheIdentifierRule(t *testing.T) {
	for _, id := range []string{"", " roster", "roster\n"} {
		if err := policystate.InitAuthority(t.Context(), newDir(t), id); !errors.Is(err, policystate.ErrAuthorityInvalid) {
			t.Errorf("InitAuthority(%q) = %v, want ErrAuthorityInvalid", id, err)
		}
	}
}
