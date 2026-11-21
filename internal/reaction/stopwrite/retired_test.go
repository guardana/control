package stopwrite_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stopwrite"
)

// deadlineStopOf is stopOf under DEADLINE_EXCEEDED at version "1", lasting
// ten minutes.
func deadlineStopOf(t *testing.T, finding, run string, created time.Time) reaction.Stop {
	t.Helper()
	s := stopOf(t, finding, run, created)
	s.RuleID, s.RuleVersion, s.ExpiresAt = "DEADLINE_EXCEEDED", "1", created.Add(10*time.Minute)
	return s
}

// TestAnUpgradeLiftsNoStopOfARetiredRule: a list under a route naming
// DEADLINE_EXCEEDED at version "1", holding a stop of run-1, a stop of run-2
// and a lift of run-2, is carried into a list under the route signed again
// without it: the stop of run-1 is kept as it was and still stops run-1 on
// the plane, the lifted one is covered, and the operator's lift ends the
// kept one. No writer appends a new stop of that version.
func TestAnUpgradeLiftsNoStopOfARetiredRule(t *testing.T) {
	from, to := routeNamingAlso(t, "DEADLINE_EXCEEDED", "1"), routeOf(t, 3, false)
	h := reaction.HeaderFor(from, "lst-0123456789abcdef0123456789abcdef")
	kept := must(t)(deadlineStopOf(t, "f-1", "run-1", clock0.Add(-time.Minute)).Marshal())
	old := writeLines(t, must(t)(h.Marshal()), kept,
		must(t)(deadlineStopOf(t, "f-2", "run-2", clock0.Add(-time.Minute)).Marshal()),
		must(t)(liftOf(t, liftKey(), from, h.ListID, "run-2", 3).Marshal()))
	dir := emptyDir(t)
	got, err := stopwrite.Carry(bg, old, from, dir, to, clock0)
	if err != nil {
		t.Fatalf("Carry from the route naming the retired version: %v", err)
	}
	if got.Stops != 1 || got.Covered != 1 || got.LeftOut != 0 {
		t.Errorf("Carry = %+v; want 1 stop and 1 covered", got)
	}
	lines := strings.Split(string(content(t, dir)), "\n")
	if len(lines) != 4 || lines[1] != string(kept) || !strings.Contains(lines[2], `"finding_id":"f-2"`) {
		t.Fatalf("the carried list:\n%s", content(t, dir))
	}
	p := planeOn(t, dir, to)
	if s, _, _ := p.Current().ForSubject(reaction.Subject{Tenant: "acme", Run: "run-1"}, clock0, time.Time{}); s != reaction.Stopped {
		t.Errorf("run-1 on the carried list: %s, want %s", s, reaction.Stopped)
	}
	expectRetiredRefusedThenLifted(t, dir, to, lines[0])
}

// expectRetiredRefusedThenLifted fails unless the list in dir under route
// to, whose header line is headLine, refuses a new stop of DEADLINE_EXCEEDED
// "1" from every writer, left unchanged, and takes a lift of run-1's kept stop.
func expectRetiredRefusedThenLifted(t *testing.T, dir string, to reaction.Route, headLine string) {
	t.Helper()
	before := content(t, dir)
	stale := deadlineStopOf(t, "f-3", "run-3", clock0)
	if _, err := stopwrite.AppendStop(bg, dir, to, stale, clock0); !errors.Is(err, stopwrite.ErrRefused) || !errors.Is(err, reaction.ErrStopRetired) {
		t.Errorf("AppendStop of a stop of the retired version: %v", err)
	}
	if _, _, err := appendFinding(bg, dir, to, stale, clock0); !errors.Is(err, stopwrite.ErrRefused) || !errors.Is(err, reaction.ErrStopRetired) {
		t.Errorf("AppendFinding of a stop of the retired version: %v", err)
	}
	if !bytes.Equal(content(t, dir), before) {
		t.Fatal("a refused append changed the list")
	}
	head, err := reaction.ParseLine([]byte(headLine))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := stopwrite.AppendLift(bg, dir, to, liftOf(t, liftKey(), to, head.Header.ListID, "run-1", 2), clock0); err != nil || n != 4 {
		t.Errorf("a lift of the kept stop: line %d, %v", n, err)
	}
}
