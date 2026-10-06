package main

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// TestEveryLineReactWritesIsOneThePlaneAccepts: over random findings logs
// and lifts, a plane's poller, which judges each read from the prefix it
// accepted, never finds the list unknown; the list names exactly the
// findings that may stop a run, as this test decides that from how it built
// them; and no run has two active stops.
func TestEveryLineReactWritesIsOneThePlaneAccepts(t *testing.T) {
	for seed := range uint64(4) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 0x5eed)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
			reactProperty(t, rng)
		})
	}
}

// findingMaker draws findings, weighted towards ones that may stop a run,
// and records for each whether it may.
type findingMaker struct {
	rng     *rand.Rand
	pool    []string
	mayStop map[string]bool
	want    map[string]bool
	next    int
}

func (m *findingMaker) batch() []*findingv1alpha1.FindingRecord {
	verdicts := []controlv1.FindingVerdict{confirmed, confirmed, controlv1.FindingVerdict_FINDING_VERDICT_SUSPECTED,
		controlv1.FindingVerdict_FINDING_VERDICT_INDETERMINATE}
	rules := []string{denial, outside, "DEADLINE_EXCEEDED"}
	var out []*findingv1alpha1.FindingRecord
	for range 1 + m.rng.IntN(8) {
		run, rule, verdict := m.pool[m.rng.IntN(len(m.pool))], rules[m.rng.IntN(len(rules))], verdicts[m.rng.IntN(len(verdicts))]
		f := finding(m.next, run, rule, verdict)
		listed := m.rng.IntN(5) != 0
		if !listed {
			f.Procedure.Digest = strings.Repeat("d", 64)
		}
		m.want[fid(m.next)] = m.mayStop[run] && rule != "DEADLINE_EXCEEDED" && verdict == confirmed && listed
		out = append(out, f)
		m.next++
	}
	return out
}

func reactProperty(t *testing.T, rng *rand.Rand) {
	tr := newStopTree(t)
	m := &findingMaker{rng: rng, want: map[string]bool{}, next: 1,
		pool:    []string{tr.open, tr.second, tr.open, tr.second, tr.child, tr.closed, tr.alien, "run-" + strings.Repeat("e", 32)},
		mayStop: map[string]bool{tr.open: true, tr.second: true}}
	poller, err := stoplist.Open(stoplist.Options{Dir: tr.stops, Route: tr.parsed, Interval: time.Second, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	log := tr.findingsDir(t, "log")
	for pass := range 15 {
		appendLog(t, log, m.batch()...)
		now := time.Now()
		if code, stdout, stderr := tr.react(t, log, now); code != exitOK {
			t.Fatalf("pass %d: react answered %d: %q\n%s", pass, code, stderr, stdout)
		}
		if rng.IntN(3) == 0 {
			run := []string{tr.open, tr.second}[rng.IntN(2)]
			want := exitFail
			if slices.ContainsFunc(tr.judged(t, now).Entries(), func(e reaction.Entry) bool { return e.RunID == run }) {
				want = exitOK
			}
			if code, _, stderr := invoke(t, append(append([]string{"stops", "lift"}, tr.routeArgs()...),
				"--key", tr.liftKey, "--run", run, tr.stops)...); code != want {
				t.Fatalf("pass %d: stops lift answered %d: %q, want %d", pass, code, stderr, want)
			}
		}
		if s := poller.Poll(); s.State() == reaction.Unknown {
			t.Fatalf("pass %d: the plane's poller is unknown: %s: %s", pass, s.Cause(), s.Detail())
		}
		checkNamed(t, tr.judged(t, now), m.want, now)
	}
	stopping := 0
	for _, may := range m.want {
		if may {
			stopping++
		}
	}
	if len(m.want) < 20 || stopping < 5 {
		t.Fatalf("the passes made %d findings, %d of them stopping, too few to say anything", len(m.want), stopping)
	}
}

// checkNamed holds list to naming exactly the findings want says may stop,
// with each active stop of a run, in line order, outlasting the one before.
func checkNamed(t *testing.T, list reaction.List, want map[string]bool, now time.Time) {
	t.Helper()
	for id, may := range want {
		if list.Names(id) != may {
			t.Fatalf("the list names %s: %v, want %v", id, list.Names(id), may)
		}
	}
	last := map[string]time.Time{}
	for _, e := range list.Entries() {
		if !now.Before(e.ExpiresAt) {
			continue
		}
		if prev, ok := last[e.RunID]; ok && !e.ExpiresAt.After(prev) {
			t.Fatalf("run %s has a stop on line %d that does not outlast its active one, ending %s", e.RunID, e.Line, prev)
		}
		last[e.RunID] = e.ExpiresAt
	}
}
