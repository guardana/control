package reaction_test

import (
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// TestAnEntryEndsOnlyAtAClockItCanTrust: an entry stops until a usable clock,
// not behind the floor, reaches its expiry; a zero clock, one past the last
// usable instant and one behind the floor keep it stopping.
func TestAnEntryEndsOnlyAtAClockItCanTrust(t *testing.T) {
	expires := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	e := reaction.Entry{Stop: reaction.Stop{ExpiresAt: expires}}
	for _, c := range []struct {
		name       string
		now, floor time.Time
		want       bool
	}{
		{"a second before its expiry", expires.Add(-time.Second), time.Time{}, true},
		{"at its expiry", expires, time.Time{}, false},
		{"after its expiry", expires.Add(time.Hour), time.Time{}, false},
		{"a zero clock", time.Time{}, time.Time{}, true},
		{"a clock past the last usable instant", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, true},
		{"a clock behind the floor", expires.Add(time.Hour), expires.Add(2 * time.Hour), true},
		{"a clock at the floor", expires.Add(time.Hour), expires.Add(time.Hour), false},
	} {
		if got := e.ActiveAt(c.now, c.floor); got != c.want {
			t.Errorf("%s: ActiveAt = %v, want %v", c.name, got, c.want)
		}
	}
}
