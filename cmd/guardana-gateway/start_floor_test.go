package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

// expectStartRefused runs the tree and fails unless run refuses to start,
// saying each of says, and serves nothing.
func expectStartRefused(t *testing.T, tr tree, says ...string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if status := serve(context.Background(), tr.config, "", &stdout, &stderr); status != exitFail {
		t.Fatalf("run answered %d; it must refuse to start", status)
	}
	if stdout.Len() != 0 {
		t.Errorf("run wrote to standard output before refusing: %q", stdout.String())
	}
	for _, s := range says {
		if !strings.Contains(stderr.String(), s) {
			t.Errorf("the refusal %q does not say %q", stderr.String(), s)
		}
	}
}

// TestAStartAgainstTheFloorOnDiskIsRefused: once a plane raised the floor, a
// restart onto a lower bundle, onto the floor's serial with another digest,
// or onto a higher one with no statement bound to it is refused, naming both
// serials; the floor's own bundle with its statement starts.
func TestAStartAgainstTheFloorOnDiskIsRefused(t *testing.T) {
	bundleFile := func(tr tree) string { return filepath.Join(tr.dir, "policy.bundle") }
	serial3 := strings.Replace(fixtureDocument, `"serial":1`, `"serial":3`, 1)
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T, tr tree)
		says    []string
	}{
		{"a lower bundle", func(t *testing.T, tr tree) {
			writeBundle(t, bundleFile(tr), fixtureDocument)
			tr.confirm(t)
		}, []string{"policy.bundle_file", "at or below the floor", "bundle serial 1, floor serial 2"}},
		{"the floor's serial with another digest", func(t *testing.T, tr tree) {
			writeBundle(t, bundleFile(tr), strings.Replace(serial2, "2026-09-20.1", "2026-09-20.1-other", 1))
			tr.confirm(t)
		}, []string{"policy.bundle_file", "the floor's serial with another digest", "bundle serial 2, floor serial 2"}},
		{"a higher bundle without its statement", func(t *testing.T, tr tree) {
			writeBundle(t, bundleFile(tr), serial3)
		}, []string{"policy.statement_file", "above the floor", "bundle serial 3, floor serial 2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := newTree(t)
			writeBundle(t, bundleFile(tr), serial2)
			tr.confirm(t)
			tr.raiseFloor(t)
			c.arrange(t, tr)
			expectStartRefused(t, tr, c.says...)
		})
	}

	tr := newTree(t)
	writeBundle(t, bundleFile(tr), serial2)
	tr.confirm(t)
	tr.raiseFloor(t)
	tr.raiseFloor(t)
	writeBundle(t, bundleFile(tr), serial3)
	tr.confirm(t)
	tr.raiseFloor(t)
}

// TestAStartWithNoFloorIsRefused: a floor file removed after it was made and
// a volume that holds no floor directory are not a first start, and each is
// refused naming policy.state_dir.
func TestAStartWithNoFloorIsRefused(t *testing.T) {
	tr := newTree(t)
	removeFloorFiles(t, filepath.Join(tr.dir, "floors"))
	expectStartRefused(t, tr, "policy.state_dir", "no floor file")

	tr = newTree(t)
	if err := os.RemoveAll(filepath.Join(tr.dir, "floors")); err != nil {
		t.Fatal(err)
	}
	expectStartRefused(t, tr, "policy.state_dir", "not a floor directory")

	tr = newTree(t)
	empty := filepath.Join(tr.dir, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "policy.state_dir", empty)
	expectStartRefused(t, tr, "policy.state_dir", "not a floor directory")
}

// startLogged builds a serving plane over the tree and returns what it
// logged while it started.
func startLogged(t *testing.T, tr tree) string {
	t.Helper()
	var log bytes.Buffer
	p, err := build(tr.load(t), slog.New(slog.NewTextHandler(&log, nil)), time.Now(), roleServe, "")
	if err != nil {
		t.Fatalf("the plane did not start: %v", err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	return log.String()
}

// TestAStartLogsTheOperatorsLastReset: a floor the operator reset is said at
// the next start, with the reason and the floor it replaced, or that the
// reset found no file; and how the policy started is said too.
func TestAStartLogsTheOperatorsLastReset(t *testing.T) {
	tr := newTree(t)
	if logged := startLogged(t, tr); strings.Contains(logged, "reset") || !strings.Contains(logged, "the policy starts confirmed") ||
		!strings.Contains(logged, `floor="serial 1 digest sha256:`) {
		t.Fatalf("a first start logged, where the floor it raised should be named:\n%s", logged)
	}
	empty, err := policy.EmptyFloor("gateway-fixture")
	if err != nil {
		t.Fatal(err)
	}
	floors := filepath.Join(tr.dir, "floors")
	if _, err := policystate.Reset(context.Background(), floors, policystate.KindPlane, empty, "moved to a new signer"); err != nil {
		t.Fatal(err)
	}
	expectLogged(t, startLogged(t, tr), "the floor was reset by the operator", `reason="moved to a new signer"`, `from="serial 1 digest sha256:`)

	tr = newTree(t)
	tr.unconfirmed(t)
	floors = filepath.Join(tr.dir, "floors")
	removeFloorFiles(t, floors)
	if _, err := policystate.Reset(context.Background(), floors, policystate.KindPlane, empty, "the volume was restored"); err != nil {
		t.Fatal(err)
	}
	expectLogged(t, startLogged(t, tr), "the reset found no floor file", "the policy starts unconfirmed")
}

// expectLogged fails unless the log holds each of wants.
func expectLogged(t *testing.T, logged string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(logged, want) {
			t.Errorf("the start does not log %q:\n%s", want, logged)
		}
	}
}

// removeFloorFiles removes every floor file of the floor directory, and
// fails when it held none.
func removeFloorFiles(t *testing.T, floors string) {
	t.Helper()
	entries, err := os.ReadDir(floors)
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".floor.json") {
			if err := os.Remove(filepath.Join(floors, e.Name())); err != nil {
				t.Fatal(err)
			}
			removed = true
		}
	}
	if !removed {
		t.Fatal("the floor directory holds no floor file to remove")
	}
}

// TestAnExpiredStatementStartsNothingConfirmed: a plane started on a
// statement whose budget has run out starts unconfirmed, says so with the
// state /healthz will name, and leaves the floor as it was.
func TestAnExpiredStatementStartsNothingConfirmed(t *testing.T) {
	tr := newTree(t)
	writeStatement(t, filepath.Join(tr.dir, "policy.statement"), filepath.Join(tr.dir, "policy.bundle"),
		time.Now().Add(-11*time.Minute).Truncate(time.Second))
	before := tr.floorFiles(t)
	expectLogged(t, startLogged(t, tr), "the policy starts unconfirmed", "budget has run out")
	if after := tr.floorFiles(t); !sameFiles(before, after) {
		t.Error("a start on an expired statement changed the floor directory")
	}
}
