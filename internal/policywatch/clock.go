package policywatch

import (
	"math"
	"time"
)

// The clock rule's two numbers: how far the wall clock may stand below the
// mark, and how fast the mark comes down, in parts per million of the
// monotonic time since it was seen.
const (
	clockSlack      = time.Second
	markDriftPPM    = 100
	partsPerMillion = 1_000_000
)

// mark is the highest offset seen between the wall clock and the monotonic
// clock, and the monotonic reading it was seen at. Offsets are taken from the
// first pair of readings, so no wall reading is turned into a count of
// nanoseconds since 1970, which a time.Duration cannot hold far from it.
type mark struct {
	set        bool
	originWall time.Time
	originMono time.Duration
	offset     time.Duration
	at         time.Duration
}

// offsetOf is how far the wall clock moved since the first reading beyond
// what the monotonic clock did, and false for a wall reading further from the
// first than a Duration holds, which cannot be judged.
func (m *mark) offsetOf(wall time.Time, mono time.Duration) (time.Duration, bool) {
	// Round(0) drops the monotonic reading time.Now carries, which Sub would
	// otherwise use and so never see the wall clock move.
	moved := wall.Round(0).Sub(m.originWall)
	elapsed := mono - m.originMono
	if moved == math.MaxInt64 || moved < math.MinInt64+elapsed {
		return 0, false
	}
	return moved - elapsed, true
}

// back takes one pair of readings and reports whether the wall clock stands
// more than clockSlack below the mark, lowered by its drift allowance, or so
// far from the first reading that it cannot be judged. A reading at or above
// the lowered mark raises the mark to it.
func (m *mark) back(wall time.Time, mono time.Duration) bool {
	if !m.set {
		m.set, m.originWall, m.originMono, m.at = true, wall.Round(0), mono, mono
		return false
	}
	offset, judged := m.offsetOf(wall, mono)
	if !judged {
		return true
	}
	lowered := m.offset - (mono-m.at)/(partsPerMillion/markDriftPPM)
	if offset >= lowered {
		m.offset, m.at = offset, mono
		return false
	}
	return offset < lowered-clockSlack
}
