package reaction_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// randomList writes a valid list of up to 40 lines over four runs, and keeps
// beside it the entries a lift through a line leaves, computed apart from the
// judge: a stop stays unless a later lift of its run names its line or one
// after it, and a covered line, of any run, stops nothing.
func randomList(t *testing.T, rng *rand.Rand, r reaction.Route) (content []byte, ends []int, want string, findings []string) {
	t.Helper()
	b := newList(t, r)
	type stopAt struct {
		run    string
		line   int64
		lifted bool
	}
	var stops []stopAt
	lifted := map[string]bool{}
	for range rng.IntN(40) {
		run := "run-" + strconv.Itoa(rng.IntN(4))
		id := "fnd-" + strconv.Itoa(len(findings)+1)
		switch op := rng.IntN(4); {
		case op == 0:
			b.covered(id, run, clock0.Add(-time.Duration(rng.IntN(7200))*time.Second))
			findings = append(findings, id)
		case op == 1 && len(b.lines) > 1:
			through := 1 + rng.Int64N(int64(len(b.lines)))
			key := run + "@" + strconv.FormatInt(through, 10)
			if !lifted[key] {
				lifted[key] = true
				b.lift(run, through)
				for i := range stops {
					if stops[i].run == run && stops[i].line <= through {
						stops[i].lifted = true
					}
				}
			}
		default:
			created := clock0.Add(poll - time.Duration(rng.IntN(7200))*time.Second)
			line := b.stop(id, run, created, time.Duration(1+rng.IntN(3600))*time.Second)
			stops = append(stops, stopAt{run: run, line: line})
			findings = append(findings, id)
		}
		ends = append(ends, len(b.bytes()))
	}
	var left []reaction.Entry
	for _, s := range stops {
		if !s.lifted {
			left = append(left, reaction.Entry{Line: s.line, Stop: reaction.Stop{RunID: s.run}})
		}
	}
	return b.bytes(), append([]int{len(newList(t, r).bytes())}, ends...), entryRuns(left), findings
}

// TestJudgingInStepsAgreesWithJudgingAtOnce: a valid list judged whole gives
// the state, entries, prefix, usage and named findings it gives when read as
// it grew, one line at a time with a torn piece of the next line behind each
// read, each read judged from the list the read before accepted; and a line
// refused in steps is refused whole for the same cause, and leaves the steps
// where they were.
func TestJudgingInStepsAgreesWithJudgingAtOnce(t *testing.T) {
	r := listRoute(t)
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 0x5eed)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
		content, ends, want, findings := randomList(t, rng, r)
		whole, err := reaction.Judge(r, reaction.Prefix{}, content, clock0, poll)
		if err != nil {
			t.Fatalf("seed %d: the whole list: %v", seed, err)
		}
		if got := entryRuns(whole.Entries()); got != want {
			t.Fatalf("seed %d: entries %s, want %s", seed, got, want)
		}
		step := readInSteps(t, seed, rng, r, ends, content)
		if step.Accepted() != whole.Prefix() || step.Usage() != whole.Usage() ||
			!slices.EqualFunc(step.Entries(), whole.Entries(), sameEntry) {
			t.Fatalf("seed %d: in steps %s, at once %s", seed, entryRuns(step.Entries()), entryRuns(whole.Entries()))
		}
		steps := stepsList(t, r, ends, content)
		for _, f := range append(findings, "fnd-none") {
			if steps.Names(f) != whole.Names(f) || whole.Names(f) != (f != "fnd-none") {
				t.Fatalf("seed %d: %s named in steps %v, at once %v", seed, f, steps.Names(f), whole.Names(f))
			}
		}
		checkRefusedAlike(t, seed, r, step, content, findings)
	}
}

// readInSteps reads content as it grew, each read ending at one of ends with
// a torn piece of the next line behind it.
func readInSteps(t *testing.T, seed uint64, rng *rand.Rand, r reaction.Route, ends []int, content []byte) reaction.Snapshot {
	t.Helper()
	step := reaction.Snapshot{}
	for i, end := range ends {
		read := content[:end]
		if i+1 < len(ends) && ends[i+1] > end {
			read = content[:end+rng.IntN(ends[i+1]-end)]
		}
		step = step.Next(r, read, clock0, poll)
		if step.State() == reaction.Unknown {
			t.Fatalf("seed %d, read %d of %d bytes: %s %s", seed, i, len(read), step.Cause(), step.Detail())
		}
	}
	return step
}

// stepsList judges content line by line through JudgeFrom.
func stepsList(t *testing.T, r reaction.Route, ends []int, content []byte) reaction.List {
	t.Helper()
	var l reaction.List
	for _, end := range ends {
		var err error
		if l, err = reaction.JudgeFrom(r, l, content[:end], clock0, poll); err != nil {
			t.Fatalf("JudgeFrom at %d bytes: %v", end, err)
		}
	}
	return l
}

// checkRefusedAlike appends a refused line to content, judges it from step
// and whole, and then judges content from the refused read.
func checkRefusedAlike(t *testing.T, seed uint64, r reaction.Route, step reaction.Snapshot, content []byte, findings []string) {
	t.Helper()
	bad := reaction.Covered{FindingID: "fnd-other", TenantID: "other", RunID: "run-0", CreatedAt: clock0}
	wantErr := reaction.ErrCoveredRun
	if len(findings) > 0 {
		bad.FindingID, bad.TenantID = findings[len(findings)-1], "acme"
		wantErr = reaction.ErrFindingAgain
	}
	line, err := bad.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	longer := slicesConcat(content, line, []byte("\n"))
	_, wholeErr := reaction.Judge(r, reaction.Prefix{}, longer, clock0, poll)
	refused := step.Next(r, longer, clock0, poll)
	if !errors.Is(wholeErr, wantErr) || refused.State() != reaction.Unknown || refused.Cause() != reaction.CauseOf(wholeErr) ||
		refused.Detail() != wholeErr.Error() {
		t.Fatalf("seed %d: whole %v, in steps %s %q", seed, wholeErr, refused.Cause(), refused.Detail())
	}
	again := refused.Next(r, content, clock0, poll)
	if again.Accepted() != step.Accepted() || !slices.EqualFunc(again.Entries(), step.Entries(), sameEntry) {
		t.Fatalf("seed %d: after a refused read, %s; before it %s", seed, entryRuns(again.Entries()), entryRuns(step.Entries()))
	}
}

func sameEntry(a, b reaction.Entry) bool {
	x, errX := a.Marshal()
	y, errY := b.Marshal()
	return a.Line == b.Line && errX == nil && errY == nil && bytes.Equal(x, y)
}
