package recovery_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/recovery"
	"github.com/guardana/control/internal/runs"
)

type takeAll struct{}

func (takeAll) Judge(_ context.Context, r runs.SignedRecord) (runs.SignedBy, error) {
	return runs.SignedBy{Subject: "alice", KeyID: r.KeyID}, nil
}

func aliceSigner() runs.Signer {
	seed := sha256.Sum256([]byte("recovery-test/alice"))
	key := ed25519.NewKeyFromSeed(seed[:])
	return runs.Signer{KeyID: "key-alice", Sign: func(_ string, body []byte) ([]byte, error) { return ed25519.Sign(key, body), nil }}
}

// signedPlane is a plane of the signed authority at plane serial 1, roster
// serial 3 and pause serial 2, with one signed run and a pause file, and a
// backup of it.
func signedPlane(t *testing.T) (*plane, string, runs.Record) {
	t.Helper()
	p := newPlane(t, signedBlock)
	p.raiseFloor(t, 1, d1, t0)
	if err := policystate.InitAuthority(t.Context(), p.path("trust/floors"), "roster-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := policystate.RaiseAuthority(t.Context(), p.path("trust/floors"), "roster-a", 3, digestOf(3)); err != nil {
		t.Fatal(err)
	}
	if err := policystate.InitPause(t.Context(), p.path("pausefloor"), "acme", "orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := policystate.RaisePause(t.Context(), p.path("pausefloor"), "acme", "orders", 2, digestOf(2)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.path("pause/pause.json"), []byte(`{"operator":"signed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p.path("runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := runs.InitSignedAdmin(t.Context(), p.path("runs"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	rec, _, err := a.OpenSigned(t.Context(), runs.OpenRequest{Who: alice, TTL: time.Hour, Now: t0}, aliceSigner(), takeAll{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backup")
	if _, err := recovery.Backup(t.Context(), p.cfg, dir, t0); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	return p, dir, rec
}

// TestSignedRestoreWritesOnlyWhatThePlaneOwns: the floors are raised and the
// run's close carried; the operator's pause file and records are left for an
// operator to put back.
func TestSignedRestoreWritesOnlyWhatThePlaneOwns(t *testing.T) {
	p, dir, rec := signedPlane(t)
	for _, lost := range []string{"floors", "trust/floors", "pausefloor", "pause/pause.json", "runs/records/" + rec.ID + ".run.json"} {
		if err := os.RemoveAll(p.path(lost)); err != nil {
			t.Fatal(err)
		}
	}
	report, err := recovery.Restore(t.Context(), p.cfg, dir, restoreAt)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	requireSignedFloors(t, p)
	if _, err := os.Lstat(p.path("runs/state/" + rec.ID + ".closed")); err != nil {
		t.Fatalf("the run's close was not carried: %v", err)
	}
	for _, operators := range []string{"pause/pause.json", "runs/records/" + rec.ID + ".run.json"} {
		if _, err := os.Lstat(p.path(operators)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("restore wrote the operator's %s: %v", operators, err)
		}
	}
	if !slices.Equal(report.PutBack, []string{"runs.dir: records/", "pause.file"}) {
		t.Fatalf("PutBack = %q", report.PutBack)
	}
}

// requireSignedFloors holds the floors signedPlane raised to their serials,
// the plane floor stamped at the restore.
func requireSignedFloors(t *testing.T, p *plane) {
	t.Helper()
	if f := p.floor(t); f.Serial() != 1 || !f.IssuedAt().Equal(restoreAt) {
		t.Fatalf("floor = %s", describe(f))
	}
	if a, err := policystate.ReadAuthority(p.path("trust/floors"), "roster-a"); err != nil || a.Serial != 3 {
		t.Fatalf("authority floor = %+v, %v", a, err)
	}
	if pf, err := policystate.ReadPause(p.path("pausefloor"), "acme", "orders"); err != nil || pf.Serial != 2 {
		t.Fatalf("pause floor = %+v, %v", pf, err)
	}
}

// TestSignedRestoreRefusesADirectoryBackup: a backup of the directory
// authority is never restored into a signed plane, whose files it could not
// have signed.
func TestSignedRestoreRefusesADirectoryBackup(t *testing.T) {
	_, dir, _ := backedUp(t)
	p, _, _ := signedPlane(t)
	if _, err := recovery.Restore(t.Context(), p.cfg, dir, restoreAt); !errors.Is(err, recovery.ErrOtherPlane) {
		t.Fatalf("err = %v, want ErrOtherPlane", err)
	}
}
