//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/policy"
)

// raisedButRefused runs a renew of r at at, which afterRaise meddles with
// once the floor took its statement, and holds it to exit 1, the one stderr
// line want matches whole, an empty stdout, the signer floor at the statement, and --out
// holding outAfter.
func raisedButRefused(t *testing.T, r renewTree, at time.Time, meddle func(), want *regexp.Regexp, outAfter string) {
	t.Helper()
	afterRaise = meddle
	t.Cleanup(func() { afterRaise = func() {} })
	var stdout, stderr bytes.Buffer
	status := renew(r.paths(), at, &stdout, &stderr)
	afterRaise = func() {}
	if status != exitFail || stdout.Len() != 0 || !want.MatchString(stderr.String()) {
		t.Errorf("exit %d, stdout %q, stderr %q; want %d, nothing, %s", status, stdout.String(), stderr.String(), exitFail, want)
	}
	if f := signerFloor(t, r.floor); f.Serial() != 3 || !f.LatestIssuedAt().Equal(at) {
		t.Errorf("the signer floor holds %s, want serial 3 issued %s", floorText(f), at)
	}
	if got := readFile(t, r.out); got != outAfter {
		t.Errorf("--out holds %q, want %q", got, outAfter)
	}
}

// TestRenewSaysAStatementItCouldNotWriteAfterTheRaise: a write to --out that
// fails once the floor took the statement is a failure that says so, and
// leaves the earlier statement.
func TestRenewSaysAStatementItCouldNotWriteAfterTheRaise(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a directory whatever its mode")
	}
	r := newRenewTree(t)
	statements := filepath.Join(r.dir, "statements")
	if err := os.Mkdir(statements, 0o700); err != nil {
		t.Fatal(err)
	}
	r.out = filepath.Join(statements, "orders.statement")
	start := time.Now().Truncate(time.Second).Add(-time.Minute)
	if status := renew(r.paths(), start, &bytes.Buffer{}, &bytes.Buffer{}); status != exitOK {
		t.Fatalf("the first renew: exit %d", status)
	}
	before := readFile(t, r.out)
	t.Cleanup(func() { _ = os.Chmod(statements, 0o700) }) //nolint:gosec // G302: the directory back to its owner-only mode, so the test can remove it
	shut := func() {
		if err := os.Chmod(statements, 0o500); err != nil { //nolint:gosec // G302: a directory its owner may search but not write
			t.Error(err)
		}
	}
	want := regexp.MustCompile("^" + regexp.QuoteMeta(brand.CLI+": policy renew: --out "+r.out+": openat .tmp-") +
		"[0-9A-Z]+" + regexp.QuoteMeta(": permission denied; the signer floor was raised to the statement\n") + "$")
	raisedButRefused(t, r, start.Add(time.Second), shut, want, before)
}

// TestRenewRefusesAnOutThatStoppedBeingAStatement: an --out that held no
// statement once the floor took this renew's is refused and left as it is.
func TestRenewRefusesAnOutThatStoppedBeingAStatement(t *testing.T) {
	r := newRenewTree(t)
	const garbage = "not a statement\n"
	spoil := func() {
		if err := os.WriteFile(r.out, []byte(garbage), 0o600); err != nil {
			t.Error(err)
		}
	}
	want := regexp.MustCompile("^" + regexp.QuoteMeta(brand.CLI+": policy renew: --out "+r.out+
		": policykey: the statement file is not a DSSE envelope in strict JSON"+
		": strictjson: not one JSON object: it does not open an object"+
		"; the signer floor was raised to this renew's statement\n") + "$")
	raisedButRefused(t, r, time.Now().Truncate(time.Second).Add(-time.Minute), spoil, want, garbage)
}

// TestRenewWaitsForTheFloorLockOnlyAsLongAsItsBound: a floor directory whose
// lock another file holds throughout is refused once floorLockWait passes,
// with nothing raised or written.
func TestRenewWaitsForTheFloorLockOnlyAsLongAsItsBound(t *testing.T) {
	r := newRenewTree(t)
	held, err := os.Open(r.floor)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	saved := floorLockWait
	floorLockWait = 50 * time.Millisecond
	t.Cleanup(func() { floorLockWait = saved })
	run := &renewRun{}
	done := make(chan struct{})
	go func() {
		run.status = renew(r.paths(), time.Now(), &run.stdout, &run.stderr)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = held.Close()
		t.Fatal("renew still waits for the floor directory's lock past its bound")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	want := brand.CLI + ": policy renew: --floor " + r.floor +
		": context deadline exceeded; another holder kept the floor directory's lock past 50ms\n"
	if run.status != exitFail || run.stdout.Len() != 0 || run.stderr.String() != want {
		t.Errorf("exit %d, stdout %q, stderr %q; want %d, nothing, %q", run.status, run.stdout.String(), run.stderr.String(), exitFail, want)
	}
	if _, err := os.Lstat(r.out); !os.IsNotExist(err) {
		t.Errorf("--out: %v, want nothing written", err)
	}
	if f := signerFloor(t, r.floor); !f.Equal(mustEmptyFloor(t)) {
		t.Errorf("the signer floor holds %s, want no serial", floorText(f))
	}
}

func mustEmptyFloor(t *testing.T) policy.Floor {
	t.Helper()
	f, err := policy.EmptyFloor("orders-policy")
	if err != nil {
		t.Fatal(err)
	}
	return f
}
