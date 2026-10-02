package policystate_test

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policystate"
)

// The file names of the two bundle ids these tests use: the hex SHA-256 of
// each id, computed apart from the code under test.
const (
	fileA = "73449d42e9b6b083761e50cfdf6fbe631946f4c12dd02ac8e5c64eaedeb2850f.floor.json"
	fileB = "d8832a1b44e0743b0465f2c7daa51efa12d7c5538fbc27240b46adaeb465ab87.floor.json"
)

const (
	idA = "bundle-a"
	idB = "bundle-b"
	d3  = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	d5  = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
	d6  = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	d7  = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
)

// emptyBodyA is the floor file Init writes for idA.
const emptyBodyA = `{"schema_version":"1.0","bundle_id":"bundle-a","serial":null,"digest":null,"issued_at":null,"latest_issued_at":null,"reset_reason":null,"reset_from":null}` + "\n"

// leftover is a name internal/files gives a temporary file: what a crash
// during a write leaves behind.
const leftover = ".tmp-ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// at is 2026-09-11 at the time of day given as "15:04:05".
func at(t testing.TB, clock string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, "2026-09-11T"+clock+"Z")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// noon is the clock reading every raise in these tests is judged at, unless a
// test names another.
func noon(t testing.TB) time.Time { return at(t, "12:00:00") }

var freshKey = ed25519.NewKeyFromSeed([]byte("policystate-test-freshness-key-1"))

// statement is a verified freshness statement naming these values.
func statement(t testing.TB, id string, serial int64, digest, issued string) policy.Statement {
	t.Helper()
	env, err := policy.SignStatement(id, serial, digest, at(t, issued), freshKey, "fresh")
	if err != nil {
		t.Fatalf("signing a statement this test builds as valid: %v", err)
	}
	st, err := policy.VerifyStatement(env, bundle.Keyring{"fresh": freshKey.Public().(ed25519.PublicKey)})
	if err != nil {
		t.Fatalf("verifying a statement this test signed: %v", err)
	}
	return st
}

// floorAt3 is a floor of idA holding serial 3 issued at 09:00 and renewed at
// latest.
func floorAt3(t testing.TB, latest string) policy.Floor {
	t.Helper()
	f, err := policy.NewFloor(idA, 3, d3, at(t, "09:00:00"), at(t, latest))
	if err != nil {
		t.Fatalf("NewFloor refused a floor this test builds as valid: %v", err)
	}
	return f
}

// newDir is a path under a fresh temporary directory where nothing stands yet.
func newDir(t testing.TB) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "floors")
}

// initDir is a directory of kind with idA's floor file holding no serial yet.
func initDir(t testing.TB, kind policystate.Kind) string {
	t.Helper()
	dir := newDir(t)
	if err := policystate.Init(t.Context(), dir, kind, idA); err != nil {
		t.Fatalf("Init(%s): %v", dir, err)
	}
	return dir
}

func open(t testing.TB, dir string) *policystate.Store {
	t.Helper()
	s, err := policystate.Open(dir, policystate.KindPlane)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// raised is a plane's directory whose idA floor holds serial 5, issued at
// 10:00 and renewed at 11:00.
func raised(t testing.TB) (string, *policystate.Store) {
	t.Helper()
	dir := initDir(t, policystate.KindPlane)
	s := open(t, dir)
	for _, issued := range []string{"10:00:00", "11:00:00"} {
		if _, err := s.Raise(t.Context(), statement(t, idA, 5, d5, issued), noon(t)); err != nil {
			t.Fatalf("Raise to serial 5 at %s: %v", issued, err)
		}
	}
	return dir, s
}

func readFile(t testing.TB, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a file under the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeFile(t testing.TB, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// describe spells a floor the way a failure message reads it.
func describe(f policy.Floor) string {
	if !f.HasSerial() {
		return fmt.Sprintf("%s with no serial", f.BundleID())
	}
	return fmt.Sprintf("%s serial %d %s issued %s latest %s", f.BundleID(), f.Serial(), f.Digest(),
		policy.FormatIssuedAt(f.IssuedAt()), policy.FormatIssuedAt(f.LatestIssuedAt()))
}
