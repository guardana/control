package reaction_test

import (
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// TestTwoReadsFromOneListDoNotShareTheirLines: a list two judges resume from
// is left as it was, and neither judge's lines reach the other's list.
func TestTwoReadsFromOneListDoNotShareTheirLines(t *testing.T) {
	r := listRoute(t)
	b := newList(t, r)
	b.stop("fnd-a1", "run-a", clock0, time.Hour)
	b.stop("fnd-a2", "run-a", clock0, time.Hour)
	b.stop("fnd-a3", "run-a", clock0, time.Hour)
	// Judged a line at a time, as a plane reads a growing list, so the base
	// holds room past its own entries for a careless append to write into.
	var base reaction.List
	for n := 1; n <= len(b.lines); n++ {
		var err error
		if base, err = reaction.JudgeFrom(r, base, b.prefixOf(n), clock0, poll); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
	}
	withB, withC := newList(t, r), newList(t, r)
	withB.lines = append(withB.lines[:0], b.lines...)
	withC.lines = append(withC.lines[:0], b.lines...)
	withB.stop("fnd-b1", "run-b", clock0, time.Hour)
	withC.stop("fnd-c1", "run-c", clock0, time.Hour)
	lb, err := reaction.JudgeFrom(r, base, withB.bytes(), clock0, poll)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := reaction.JudgeFrom(r, base, withC.bytes(), clock0, poll)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		l    reaction.List
		want string
		not  string
	}{
		{"the base", base, "run-a@2,run-a@3,run-a@4", "fnd-b1"},
		{"the read with run-b", lb, "run-a@2,run-a@3,run-a@4,run-b@5", "fnd-c1"},
		{"the read with run-c", lc, "run-a@2,run-a@3,run-a@4,run-c@5", "fnd-b1"},
	} {
		if got := entryRuns(c.l.Entries()); got != c.want || c.l.Names(c.not) {
			t.Errorf("%s: entries %s, names %s %v; want %s", c.name, got, c.not, c.l.Names(c.not), c.want)
		}
	}
	if base.Names("fnd-c1") {
		t.Error("the base names fnd-c1")
	}
	withB.lift("run-a", 4)
	lifted, err := reaction.JudgeFrom(r, lb, withB.bytes(), clock0, poll)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryRuns(lifted.Entries()); got != "run-b@5" {
		t.Errorf("the read with run-b, its run-a lifted: entries %s, want run-b@5", got)
	}
}

// TestAResumedReadHoldsTheLinesBeforeToTheClock: a clock read behind an
// accepted line's created_at refuses the list when no line was added, as
// judging it whole at that clock does.
func TestAResumedReadHoldsTheLinesBeforeToTheClock(t *testing.T) {
	r := listRoute(t)
	b := newList(t, r)
	b.stop("fnd-a1", "run-a", clock0.Add(-time.Minute), time.Hour)
	b.stop("fnd-a2", "run-a", clock0.Add(poll), time.Hour)
	b.stop("fnd-a3", "run-a", clock0, time.Hour)
	content := b.bytes()
	accepted := reaction.Snapshot{}.Next(r, content, clock0, poll)
	if accepted.State() != reaction.Stopped {
		t.Fatalf("at clock0: %s %s", accepted.State(), accepted.Detail())
	}
	back := clock0.Add(-time.Second)
	_, whole := reaction.Judge(r, reaction.Prefix{}, content, back, poll)
	expectOnly(t, "judged whole a second back", whole, reaction.ErrDatedAhead, judgeRefusals())
	again := accepted.Next(r, content, back, poll)
	if again.State() != reaction.Unknown || again.Cause() != reaction.CauseDatedAhead || !strings.HasPrefix(again.Detail(), "line 3:") {
		t.Fatalf("resumed a second back: %s %s %q", again.State(), again.Cause(), again.Detail())
	}
	if same := accepted.Next(r, content, clock0, poll); same.State() != reaction.Stopped || same.Accepted() != accepted.Accepted() {
		t.Fatalf("resumed at clock0: %s", same.State())
	}
}

// TestAResumedReadHoldsItsHeaderToTheRoute: the list accepted under one
// route is refused under another, as its header line is.
func TestAResumedReadHoldsItsHeaderToTheRoute(t *testing.T) {
	r := listRoute(t)
	b := newList(t, r)
	b.stop("fnd-a1", "run-a", clock0, time.Hour)
	base := mustJudge(t, b.bytes())
	other := validRoute(t)
	_, whole := reaction.Judge(other, reaction.Prefix{}, b.bytes(), clock0, poll)
	expectOnly(t, "judged whole under another route", whole, reaction.ErrListRoute, judgeRefusals())
	_, err := reaction.JudgeFrom(other, base, b.bytes(), clock0, poll)
	expectOnly(t, "resumed under another route", err, reaction.ErrListRoute, judgeRefusals())
}
