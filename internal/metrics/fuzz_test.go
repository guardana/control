package metrics

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/metrics/metricstest"
	"github.com/guardana/control/internal/pause"
)

// FuzzRender holds the text to the strict reader for any cause, any signed
// count and any wait: a reading Render takes reads back, a declared cause
// under its own label and any other under other, and one it refuses is one
// with a negative count.
func FuzzRender(f *testing.F) {
	f.Add(int64(0), uint64(0), "stale", uint64(1))
	f.Add(int64(-1), uint64(1_500), "a \"quoted\" \\ cause\nover two lines", uint64(7))
	f.Add(int64(3), uint64(1<<62+1), "\xff", uint64(0))
	f.Add(int64(0), uint64(0), "other", uint64(18446744073709551615))
	f.Fuzz(func(t *testing.T, sent int64, micros uint64, cause string, n uint64) {
		var r Reading
		r.Adapter.Sent = sent
		r.Pipeline.Asks.Micros = micros
		r.Pause.Failed = map[pause.Cause]uint64{pause.Cause(cause): n}
		text, err := Render(r)
		if wantErr := sent < 0; (err != nil) != wantErr {
			t.Fatalf("Render answered %v for sent %d and cause %q", err, sent, cause)
		}
		if err != nil {
			return
		}
		families, err := metricstest.Parse(text)
		if err != nil {
			t.Fatalf("the strict reader refused the text: %v\n%s", err, text)
		}
		label := Other
		if slices.Contains(pause.Causes(), pause.Cause(cause)) {
			label = cause
		}
		failures, _ := metricstest.Find(families, Prefix()+"pause_poll_failures_total")
		if s, ok := failures.One(map[string]string{"cause": label}); !ok || s.Raw != strconv.FormatUint(n, 10) || len(failures.Samples) != 1 {
			t.Errorf("cause %q with %d read back as %+v", cause, n, failures.Samples)
		}
		wait, _ := metricstest.Find(families, Prefix()+"pdp_ask_wait_seconds_total")
		if s, ok := wait.One(map[string]string{}); !ok || microsOf(s.Raw) != strconv.FormatUint(micros, 10) {
			t.Errorf("%d microseconds read back as %+v", micros, wait.Samples)
		}
	})
}

// microsOf spells a decimal count of seconds as a count of microseconds,
// digit by digit, so no float rounds either side of the comparison.
func microsOf(raw string) string {
	whole, frac, _ := strings.Cut(raw, ".")
	if len(frac) > 6 {
		return "more than six decimals: " + raw
	}
	digits := strings.TrimLeft(whole+frac+strings.Repeat("0", 6-len(frac)), "0")
	if digits == "" {
		return "0"
	}
	return digits
}
