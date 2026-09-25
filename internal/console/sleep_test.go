package console

import (
	"net/http"
	"testing"
	"time"
	"unsafe"
)

// timeWords is how time.Time lays out its fields: with a monotonic reading
// the second word holds it.
type timeWords struct {
	wall uint64
	ext  int64
	loc  *time.Location
}

// skewed is from with its wall clock moved by d and its monotonic reading
// left as it was, which is what a clock reads after a machine that stops its
// monotonic clock while asleep slept for d. It fails the test when this Go
// lays out time.Time otherwise, so a skew that could not be built is never
// read as a check that passed.
func skewed(t *testing.T, from time.Time, d time.Duration) time.Time {
	t.Helper()
	moved := from.Add(d)
	(*timeWords)(unsafe.Pointer(&moved)).ext = (*timeWords)(unsafe.Pointer(&from)).ext //nolint:gosec // G103: the one way to build a reading a sleeping machine produces
	if moved.Sub(from) != 0 || moved.Round(0).Sub(from.Round(0)) != d {
		t.Fatalf("the skew did not take: the monotonic clock moved %s and the wall %s", moved.Sub(from), moved.Round(0).Sub(from.Round(0)))
	}
	return moved
}

// TestThePrintedTokensLifeHoldsOnBothClocks: the page's start is a reading
// with a monotonic clock. A trade whose wall clock is past the ten minutes
// is refused though the monotonic clock says no time passed, and one whose
// monotonic clock is past them is refused though the wall was set back; a
// trade short of them on both is taken.
func TestThePrintedTokensLifeHoldsOnBothClocks(t *testing.T) {
	dir, _ := newPlane(t)
	start := time.Now()
	for name, c := range map[string]struct {
		at   time.Time
		want int
	}{
		"eleven minutes asleep":             {skewed(t, start, 11*time.Minute), http.StatusUnauthorized},
		"eleven minutes, the wall set back": {skewed(t, start.Add(11*time.Minute), -10*time.Minute), http.StatusUnauthorized},
		"nine minutes asleep":               {skewed(t, start, 9*time.Minute), http.StatusOK},
	} {
		clock := &fakeClock{now: start}
		s := serveAt(t, dir, "", clock.read)
		clock.set(c.at)
		if a := s.do(t, s.tradeCall()); a.status != c.want {
			t.Errorf("%s: the trade answered %d %q, want %d", name, a.status, a.body, c.want)
		}
	}
}
