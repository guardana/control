package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

// raceWindow is how long the first renew keeps the floor's lock once the
// second has started: ample for the second to reach the lock and, were the
// lock not held, to raise and write.
const raceWindow = 200 * time.Millisecond

// paths is what renew is told in this tree.
func (r renewTree) paths() renewPaths {
	return renewPaths{key: r.freshKey, bundle: r.bundle, bundlePublicKey: r.bundlePub, floor: r.floor, out: r.out}
}

// renewRun is one renew's exit status and output.
type renewRun struct {
	status         int
	stdout, stderr bytes.Buffer
}

// renewDuringRenew runs a renew of r's bundle at first and, from inside it
// once it raised the floor, starts a second renew of p at second. held
// reports whether the second neither raised nor finished in raceWindow while
// the first stood there.
func renewDuringRenew(t *testing.T, r renewTree, first time.Time, p renewPaths, second time.Time) (a, b *renewRun, held bool) {
	t.Helper()
	a, b = &renewRun{}, &renewRun{}
	secondRaised, secondDone := make(chan struct{}), make(chan struct{})
	started := false
	afterRaise = func() {
		started = true
		afterRaise = func() { close(secondRaised) }
		go func() {
			b.status = renew(p, second, &b.stdout, &b.stderr)
			close(secondDone)
		}()
		select {
		case <-secondRaised:
		case <-secondDone:
		case <-time.After(raceWindow):
			held = true
		}
	}
	a.status = renew(r.paths(), first, &a.stdout, &a.stderr)
	if started {
		select {
		case <-secondDone:
		case <-time.After(2 * floorLockWait):
			t.Fatal("the second renew never finished")
		}
	}
	afterRaise = func() {}
	if !started {
		t.Fatalf("the first renew never raised: exit %d, stderr %q", a.status, a.stderr.String())
	}
	return a, b, held
}

// TestRenewOrdersTwoRenewsOfOneFloor: a renew started while another holds the
// signer floor's lock neither raises nor finishes until the first wrote its
// statement, and once both finish --out holds the newer statement and the
// floor holds it too. A second renew of the first's own statement writes it
// again; one whose clock reads before it is refused by the floor and leaves
// it.
func TestRenewOrdersTwoRenewsOfOneFloor(t *testing.T) {
	start := time.Now().Truncate(time.Second).Add(-time.Minute)
	higher := func(r renewTree) renewPaths {
		path := filepath.Join(r.dir, "higher.bundle")
		writeBundle(t, path, renewDocument(4, "allow-reads"), r.bundleSigner)
		p := r.paths()
		p.bundle = path
		return p
	}
	same := func(r renewTree) renewPaths { return r.paths() }
	for name, c := range map[string]renewOrder{
		"a higher serial":    {higher, start, exitOK, "", 4, start},
		"a later time":       {same, start.Add(time.Second), exitOK, "", 3, start.Add(time.Second)},
		"the same statement": {same, start, exitOK, "", 3, start},
		"an older time":      {same, start.Add(-time.Second), exitFail, string(policy.ErrClockBehindFloor), 3, start},
	} {
		r := newRenewTree(t)
		first, second, held := renewDuringRenew(t, r, start, c.second(r), c.at)
		if !held {
			t.Errorf("%s: the second renew raised or finished while the first held the floor's lock", name)
		}
		c.check(t, name, r, first, second)
	}
}

// renewOrder is a second renew started while the first holds the floor's
// lock, and what both leave.
type renewOrder struct {
	second  func(r renewTree) renewPaths
	at      time.Time
	status  int
	refusal string
	serial  int64
	want    time.Time
}

func (c renewOrder) check(t *testing.T, name string, r renewTree, first, second *renewRun) {
	t.Helper()
	if first.status != exitOK || first.stderr.Len() != 0 {
		t.Errorf("%s: the first renew: exit %d, stderr %q", name, first.status, first.stderr.String())
	}
	if second.status != c.status || !strings.Contains(second.stderr.String(), c.refusal) || strings.Contains(second.stderr.String(), errRaceLost) {
		t.Errorf("%s: the second renew: exit %d, stderr %q; want %d and %q", name, second.status, second.stderr.String(), c.status, c.refusal)
	}
	if name == "the same statement" && second.stdout.String() != first.stdout.String() {
		t.Errorf("%s: the second renew printed %q, the first %q", name, second.stdout.String(), first.stdout.String())
	}
	c.checkLeft(t, name, r)
}

// checkLeft holds --out and the signer floor to the newer statement.
func (c renewOrder) checkLeft(t *testing.T, name string, r renewTree) {
	t.Helper()
	_, st := freshStatement(t, r.out)
	if st.Serial() != c.serial || !st.IssuedAt().Equal(c.want) {
		t.Errorf("%s: --out holds serial %d issued %s, want %d issued %s", name, st.Serial(), st.IssuedAt(), c.serial, c.want)
	}
	if f := signerFloor(t, r.floor); f.Serial() != st.Serial() || f.Digest() != st.Digest() || !f.LatestIssuedAt().Equal(st.IssuedAt()) {
		t.Errorf("%s: the signer floor holds %s, --out serial %d issued %s", name, floorText(f), st.Serial(), st.IssuedAt())
	}
}

// TestRenewKeepsANewerStatementAnotherFloorWrote: a renew whose floor took
// its statement, and which finds at --out a newer statement for its bundle
// id that a renew under another signer floor directory wrote, refuses and
// leaves that one; an older one it replaces.
func TestRenewKeepsANewerStatementAnotherFloorWrote(t *testing.T) {
	start := time.Now().Truncate(time.Second).Add(-time.Minute)
	for name, c := range map[string]struct {
		serial     int
		at         time.Time
		status     int
		wantSerial int64
		want       time.Time
	}{
		"a higher serial": {4, start, exitFail, 4, start},
		"a later time":    {3, start.Add(time.Second), exitFail, 3, start.Add(time.Second)},
		"an older time":   {3, start.Add(-time.Second), exitOK, 3, start},
	} {
		r := newRenewTree(t)
		other := r.paths()
		other.floor = filepath.Join(r.dir, "other-floors")
		if err := policystate.Init(context.Background(), other.floor, policystate.KindSigner, "orders-policy"); err != nil {
			t.Fatal(err)
		}
		other.bundle = filepath.Join(r.dir, "other.bundle")
		writeBundle(t, other.bundle, renewDocument(c.serial, "allow-reads"), r.bundleSigner)
		var otherErr, stdout, stderr bytes.Buffer
		if code := renew(other, c.at, &bytes.Buffer{}, &otherErr); code != exitOK {
			t.Fatalf("%s: the renew under the other floor: exit %d, stderr %q", name, code, otherErr.String())
		}
		status := renew(r.paths(), start, &stdout, &stderr)
		want := ""
		if c.status == exitFail {
			want = brand.CLI + ": policy renew: --out " + r.out +
				": it holds a newer statement for the bundle id, which another renew wrote meanwhile; it was left as it is" +
				"; the signer floor was raised to this renew's statement\n"
		}
		if status != c.status || stderr.String() != want || (c.status == exitFail) != (stdout.Len() == 0) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d and %q", name, status, stdout.String(), stderr.String(), c.status, want)
		}
		if _, st := freshStatement(t, r.out); st.Serial() != c.wantSerial || !st.IssuedAt().Equal(c.want) {
			t.Errorf("%s: --out holds serial %d issued %s, want %d issued %s", name, st.Serial(), st.IssuedAt(), c.wantSerial, c.want)
		}
		if f := signerFloor(t, r.floor); f.Serial() != 3 || !f.LatestIssuedAt().Equal(start) {
			t.Errorf("%s: the signer floor holds %s, want serial 3 issued %s", name, floorText(f), start)
		}
	}
}
