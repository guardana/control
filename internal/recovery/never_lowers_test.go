package recovery_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/recovery"
	"github.com/guardana/control/internal/runs"
)

// digestOf is the one digest of serial n in these tests, so two sides at one
// serial never fork.
func digestOf(n int64) string { return fmt.Sprintf("sha256:%064d", n) }

// rank orders the reads as the join must, unknown above every other, apart
// from the code under test.
var rank = map[controlv1.Sensitivity]int{
	controlv1.Sensitivity_SENSITIVITY_PUBLIC: 1, controlv1.Sensitivity_SENSITIVITY_INTERNAL: 2,
	controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL: 3, controlv1.Sensitivity_SENSITIVITY_RESTRICTED: 4,
	controlv1.Sensitivity_SENSITIVITY_SECRET: 5, controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED: 6,
}

type side struct {
	serial int64 // 0: no floor directory
	state  runs.State
	pauses []string
}

func randomSide(r *rand.Rand, floor bool) side {
	s := side{state: runs.State{Untrusted: r.IntN(2) == 1, MaxRead: controlv1.Sensitivity(r.Int32N(6))}}
	if floor || r.IntN(4) > 0 {
		s.serial = 1 + r.Int64N(4)
	}
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		if r.IntN(2) == 1 {
			s.pauses = append(s.pauses, id)
		}
	}
	return s
}

// put makes the plane's floor, the run's state and the pause file s's.
func (p *plane) put(t *testing.T, root string, s side) {
	t.Helper()
	if err := os.RemoveAll(p.path("floors")); err != nil {
		t.Fatal(err)
	}
	if s.serial > 0 {
		p.raiseFloor(t, s.serial, digestOf(s.serial), t0)
	}
	p.setState(t, root, s.state)
	if err := os.RemoveAll(p.path("pause/pause.json")); err != nil {
		t.Fatal(err)
	}
	if err := pause.Init(t.Context(), p.path("pause/pause.json")); err != nil {
		t.Fatal(err)
	}
	for _, id := range s.pauses {
		p.addPause(t, id)
	}
}

// TestRestoreNeverLowers draws a backup and a live state per class and
// requires each restored class at the higher of the two: the floor's serial,
// the run's taint and read, the union of the pauses.
func TestRestoreNeverLowers(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 22)) //nolint:gosec // G404: a seeded draw, the same on every run
	for i := range 16 {
		bk, live := randomSide(r, true), randomSide(r, false)
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			p := newPlane(t, directoryBlock)
			rec := p.openRun(t, bk.state)
			p.put(t, rec.ID, bk)
			dir := filepath.Join(t.TempDir(), "backup")
			if _, err := recovery.Backup(t.Context(), p.cfg, dir, t0); err != nil {
				t.Fatal(err)
			}
			p.put(t, rec.ID, live)
			if _, err := recovery.Restore(t.Context(), p.cfg, dir, restoreAt); err != nil {
				t.Fatalf("backup %+v live %+v: %v", bk, live, err)
			}
			if f := p.floor(t); f.Serial() != max(bk.serial, live.serial) || !f.IssuedAt().Equal(restoreAt) {
				t.Errorf("floor = %s, want serial %d stamped at the restore", describe(f), max(bk.serial, live.serial))
			}
			_, s := p.run(t, rec.ID)
			if s.Untrusted != (bk.state.Untrusted || live.state.Untrusted) || rank[s.MaxRead] != max(rank[bk.state.MaxRead], rank[live.state.MaxRead]) {
				t.Errorf("state = %+v from backup %+v and live %+v", s, bk.state, live.state)
			}
			want := slices.Clone(live.pauses)
			for _, id := range bk.pauses {
				if !slices.Contains(want, id) {
					want = append(want, id)
				}
			}
			slices.Sort(want)
			got := p.pauses(t)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("pauses = %v, want %v", got, want)
			}
		})
	}
}
