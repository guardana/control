package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

// A digest and a time a floor can hold, written out rather than derived.
const (
	resetDigest   = "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	resetIssuedAt = "2026-10-01T12:00:00Z"
)

func initArgs(kind, dir string) []string {
	return []string{"policy", "state", "init", "--kind", kind, "--bundle-id", "orders-policy", dir}
}

// resetArgs is a whole reset to serial 4 of orders-policy, with extra flags
// after the others.
func resetArgs(kind, dir string, extra ...string) []string {
	args := []string{"policy", "state", "reset", "--kind", kind, "--bundle-id", "orders-policy",
		"--serial", "4", "--digest", resetDigest, "--issued-at", resetIssuedAt, "--reason", "rolled back to serial 4"}
	return append(append(args, extra...), dir)
}

func initialised(t *testing.T, kind, dir string) {
	t.Helper()
	code, stdout, stderr := invoke(t, initArgs(kind, dir)...)
	if code != exitOK || stderr != "" {
		t.Fatalf("init: exit %d, stderr %q", code, stderr)
	}
	if want := "bundle_id: orders-policy\nfloor: no serial\n"; stdout != want {
		t.Errorf("init printed %q, want %q", stdout, want)
	}
}

func floorRecord(t *testing.T, kind policystate.Kind, dir string) policystate.Record {
	t.Helper()
	s, err := policystate.Open(dir, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rec, err := s.Record(context.Background(), "orders-policy")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// stateRefused runs a state command and holds it to the exit status, one
// stderr line under the command's name holding want, and nothing on stdout.
func stateRefused(t *testing.T, name string, status int, want string, args ...string) {
	t.Helper()
	code, stdout, stderr := invoke(t, args...)
	prefix := brand.CLI + ": " + strings.Join(args[:3], " ") + ": "
	line := oneStderrLine(t, stderr)
	if code != status || stdout != "" || !strings.HasPrefix(line, prefix) || !strings.Contains(line, want) {
		t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d and a line holding %q", name, code, stdout, line, status, want)
	}
}

// TestStateInitMakesAnEmptyFloor: init makes the directory, owner-only, of
// the kind named, holding the bundle id's floor with no serial yet.
func TestStateInitMakesAnEmptyFloor(t *testing.T) {
	for _, kind := range []policystate.Kind{policystate.KindPlane, policystate.KindSigner} {
		dir := filepath.Join(t.TempDir(), "floors")
		initialised(t, string(kind), dir)
		if rec := floorRecord(t, kind, dir); rec.Floor.HasSerial() || rec.Floor.BundleID() != "orders-policy" || rec.Reset != nil {
			t.Errorf("%s: the floor is %+v", kind, rec)
		}
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: the directory: %v, mode %v", kind, err, info.Mode())
		}
	}
}

// TestStateInitNeverReplacesAFloor: a second init of the same id is refused
// and the serial a reset left stays; a directory of the other kind is
// refused and gains no file.
func TestStateInitNeverReplacesAFloor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floors")
	initialised(t, "signer", dir)
	if code, _, stderr := invoke(t, resetArgs("signer", dir)...); code != exitOK {
		t.Fatalf("reset: exit %d, %q", code, stderr)
	}
	stateRefused(t, "an existing floor file", exitFail, string(policystate.ErrExists), initArgs("signer", dir)...)
	if f := floorRecord(t, policystate.KindSigner, dir).Floor; f.Serial() != 4 {
		t.Errorf("the floor holds serial %d after a refused init, want 4", f.Serial())
	}
	stateRefused(t, "a directory of the other kind", exitFail, string(policystate.ErrWrongKind), initArgs("plane", dir)...)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Errorf("the directory holds %d entries, %v; want the marker and one floor", len(entries), err)
	}
	stateRefused(t, "a kind that is neither", exitFail, string(policystate.ErrKind), initArgs("signers", filepath.Join(t.TempDir(), "x"))...)
}

// TestStateResetRecordsWhyAndWhatItReplaced: a reset to a serial, then to no
// serial, each print the floor replaced and the new one, and the file keeps
// the reason and the prior value.
func TestStateResetRecordsWhyAndWhatItReplaced(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floors")
	initialised(t, "plane", dir)
	to := "serial 4 digest " + resetDigest + " issued_at " + resetIssuedAt + " latest_issued_at " + resetIssuedAt
	resetPrints(t, resetArgs("plane", dir), "bundle_id: orders-policy\nfrom: no serial\nto: "+to+"\n")
	rec := floorRecord(t, policystate.KindPlane, dir)
	issued, _ := time.Parse(time.RFC3339, resetIssuedAt)
	if f := rec.Floor; f.Serial() != 4 || f.Digest() != resetDigest || !f.IssuedAt().Equal(issued) || !f.LatestIssuedAt().Equal(issued) {
		t.Errorf("the floor is %+v", f)
	}
	if rec.Reset == nil || rec.Reset.Reason != "rolled back to serial 4" || rec.Reset.From.HasSerial() {
		t.Fatalf("the reset note is %+v", rec.Reset)
	}
	resetPrints(t, []string{"policy", "state", "reset", "--kind", "plane", "--bundle-id", "orders-policy",
		"--empty", "--reason", "a new authority", dir}, "bundle_id: orders-policy\nfrom: "+to+"\nto: no serial\n")
	rec = floorRecord(t, policystate.KindPlane, dir)
	if rec.Floor.HasSerial() || rec.Reset == nil || rec.Reset.Reason != "a new authority" || rec.Reset.From.Serial() != 4 {
		t.Errorf("after reset --empty the file holds %+v, %+v", rec.Floor, rec.Reset)
	}
}

// resetPrints runs a reset that must succeed and holds what it printed to
// want.
func resetPrints(t *testing.T, args []string, want string) {
	t.Helper()
	code, stdout, stderr := invoke(t, args...)
	if code != exitOK || stderr != "" {
		t.Fatalf("reset: exit %d, stderr %q", code, stderr)
	}
	if stdout != want {
		t.Errorf("reset printed %q, want %q", stdout, want)
	}
}

// TestStateResetRefusals: each leaves the floor at serial 4 as it was.
func TestStateResetRefusals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floors")
	initialised(t, "signer", dir)
	if code, _, stderr := invoke(t, resetArgs("signer", dir)...); code != exitOK {
		t.Fatalf("reset: exit %d, %q", code, stderr)
	}
	before := floorRecord(t, policystate.KindSigner, dir)
	reset := func(flags ...string) []string {
		return append(append([]string{"policy", "state", "reset"}, flags...), dir)
	}
	id := []string{"--kind", "signer", "--bundle-id", "orders-policy"}
	with := func(more ...string) []string { return append(append([]string{}, id...), more...) }
	for name, c := range map[string]struct {
		status int
		want   string
		args   []string
	}{
		"an empty reason": {exitFail, string(policystate.ErrReason), reset(with("--empty", "--reason", "")...)},
		"no reason":       {exitUsage, "--reason is missing", reset(with("--empty")...)},
		"no kind":         {exitUsage, "--kind is missing", reset("--bundle-id", "orders-policy", "--empty", "--reason", "r")},
		"no bundle id":    {exitUsage, "--bundle-id is missing", reset("--kind", "signer", "--empty", "--reason", "r")},
		"no new floor":    {exitUsage, "takes --empty, or --serial, --digest and --issued-at", reset(with("--reason", "r")...)},
		"empty and a serial": {exitUsage, "takes --empty, or --serial, --digest and --issued-at",
			reset(with("--empty", "--serial", "2", "--reason", "r")...)},
		"a serial alone": {exitUsage, "takes --empty, or --serial, --digest and --issued-at",
			reset(with("--serial", "2", "--reason", "r")...)},
		"a serial and a digest": {exitUsage, "takes --empty, or --serial, --digest and --issued-at",
			reset(with("--serial", "2", "--digest", resetDigest, "--reason", "r")...)},
		"no directory": {exitUsage, "takes the floor directory", append([]string{"policy", "state", "reset"}, with("--empty", "--reason", "r")...)},
		"another spelling of issued-at": {exitFail, "--issued-at: " + string(policy.ErrStatementIssuedAt),
			reset(with("--serial", "2", "--digest", resetDigest, "--issued-at", "2026-10-01T12:00:00.5Z", "--reason", "r")...)},
		"a digest of another form": {exitFail, string(policy.ErrFloorInvalid) + ": the digest",
			reset(with("--serial", "2", "--digest", "sha256:ABC", "--issued-at", resetIssuedAt, "--reason", "r")...)},
		"serial zero": {exitFail, string(policy.ErrFloorInvalid) + ": the serial",
			reset(with("--serial", "0", "--digest", resetDigest, "--issued-at", resetIssuedAt, "--reason", "r")...)},
		"the other kind": {exitFail, string(policystate.ErrWrongKind),
			reset("--kind", "plane", "--bundle-id", "orders-policy", "--empty", "--reason", "r")},
		"an id with no file": {exitFail, string(policystate.ErrNoFloor),
			reset("--kind", "signer", "--bundle-id", "another-policy", "--empty", "--reason", "r")},
	} {
		stateRefused(t, name, c.status, c.want, c.args...)
		if after := floorRecord(t, policystate.KindSigner, dir); !after.Floor.Equal(before.Floor) || after.Reset.Reason != before.Reset.Reason {
			t.Errorf("%s: the floor moved to %+v", name, after)
		}
	}
}

// TestStateInitUsageErrors: a missing flag or directory is a usage error.
func TestStateInitUsageErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floors")
	for name, c := range map[string]struct {
		want string
		args []string
	}{
		"no kind":           {"--kind is missing", []string{"policy", "state", "init", "--bundle-id", "orders-policy", dir}},
		"no bundle id":      {"--bundle-id is missing", []string{"policy", "state", "init", "--kind", "signer", dir}},
		"no directory":      {"takes the floor directory", []string{"policy", "state", "init", "--kind", "signer", "--bundle-id", "orders-policy"}},
		"two directories":   {"takes the floor directory", append(initArgs("signer", dir), dir)},
		"a flag after it":   {"takes the floor directory", append(initArgs("signer", dir), "--kind", "plane")},
		"reset's own flags": {"flag provided but not defined: -empty", append(initArgs("signer", dir)[:7], "--empty", dir)},
	} {
		code, stdout, stderr := invoke(t, c.args...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d and %q", name, code, stdout, stderr, exitUsage, c.want)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a usage error made the directory")
	}
}
