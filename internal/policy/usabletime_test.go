package policy_test

import (
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
)

// TestUsableTime holds the floor and the ceiling at their instants, and far
// from them, where a reading taken through UnixNano would wrap.
func TestUsableTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"the zero time", time.Time{}, false},
		{"1600", time.Date(1600, time.January, 1, 0, 0, 0, 0, time.UTC), false},
		{"1677", time.Date(1677, time.June, 1, 0, 0, 0, 0, time.UTC), false},
		{"Unix -2^62 seconds", time.Unix(-1<<62, 0), false},
		{"one nanosecond before the epoch", time.Date(1969, time.December, 31, 23, 59, 59, 999999999, time.UTC), false},
		{"the epoch", time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC), true},
		{"the epoch, in another zone", time.Date(1970, time.January, 1, 2, 0, 0, 0, time.FixedZone("plus2", 2*60*60)), true},
		{"a day in 2026", time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC), true},
		{"the last nanosecond of 9999", time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC), true},
		{"the first instant of 10000", time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC), false},
		{"2262", time.Date(2262, time.June, 1, 0, 0, 0, 0, time.UTC), true},
		{"Unix 2^62 seconds", time.Unix(1<<62, 0), false},
	}
	for _, c := range cases {
		if got := policy.UsableTime(c.at); got != c.want {
			t.Errorf("UsableTime(%s) = %t, want %t", c.name, got, c.want)
		}
	}
}
