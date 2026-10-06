package reaction_test

import (
	"bytes"
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
// after it.
func randomList(t *testing.T, rng *rand.Rand, r reaction.Route) (content []byte, ends []int, want string) {
	t.Helper()
	b := newList(t, r)
	type stopAt struct {
		run    string
		line   int64
		lifted bool
	}
	var stops []stopAt
	stopped := map[string]bool{}
	lifted := map[string]bool{}
	finding := 0
	for range rng.IntN(40) {
		run := "run-" + strconv.Itoa(rng.IntN(4))
		finding++
		id := "fnd-" + strconv.Itoa(finding)
		switch op := rng.IntN(4); {
		case op == 0 && stopped[run]:
			b.covered(id, run, clock0.Add(-time.Duration(rng.IntN(7200))*time.Second))
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
			stopped[run] = true
		}
		ends = append(ends, len(b.bytes()))
	}
	var left []reaction.Entry
	for _, s := range stops {
		if !s.lifted {
			left = append(left, reaction.Entry{Line: s.line, Stop: reaction.Stop{RunID: s.run}})
		}
	}
	return b.bytes(), append([]int{len(newList(t, r).bytes())}, ends...), entryRuns(left)
}

// TestJudgingInStepsAgreesWithJudgingAtOnce: a valid list judged whole gives
// the state, entries, prefix and usage it gives when read as it grew, one
// line at a time with a torn piece of the next line behind each read.
func TestJudgingInStepsAgreesWithJudgingAtOnce(t *testing.T) {
	r := listRoute(t)
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 0x5eed)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
		content, ends, want := randomList(t, rng, r)
		whole := reaction.Snapshot{}.Next(r, content, clock0, poll)
		if whole.State() == reaction.Unknown {
			t.Fatalf("seed %d: the whole list is unknown: %s", seed, whole.Detail())
		}
		if got := entryRuns(whole.Entries()); got != want {
			t.Fatalf("seed %d: entries %s, want %s", seed, got, want)
		}
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
		if step.State() != whole.State() || step.Accepted() != whole.Accepted() || step.Usage() != whole.Usage() ||
			!slices.EqualFunc(step.Entries(), whole.Entries(), sameEntry) {
			t.Fatalf("seed %d: in steps %s %s, at once %s %s", seed, step.State(), entryRuns(step.Entries()), whole.State(), entryRuns(whole.Entries()))
		}
	}
}

func sameEntry(a, b reaction.Entry) bool {
	x, errX := a.Marshal()
	y, errY := b.Marshal()
	return a.Line == b.Line && errX == nil && errY == nil && bytes.Equal(x, y)
}
