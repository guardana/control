package reaction_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// boundList is a list within a line of MaxListBytes or MaxListLines: stops
// over fifty runs and a signed lift every hundredth line.
func boundList(tb testing.TB, r reaction.Route) []byte {
	tb.Helper()
	b := newList(tb, r)
	size := len(b.lines[0]) + 1
	for i := 0; size < reaction.MaxListBytes-reaction.MaxLiftEnvelopeBytes && len(b.lines) < reaction.MaxListLines-1; i++ {
		run := "run-" + strconv.Itoa(i%50)
		if i%100 == 99 {
			b.lift(run, int64(len(b.lines)))
		} else {
			b.stop("fnd-"+strconv.Itoa(i), run, clock0.Add(-time.Minute), time.Hour)
		}
		size += len(b.lines[len(b.lines)-1]) + 1
	}
	return b.bytes()
}

// BenchmarkPollAtTheByteBound is one poll of a list at its bound that grew by
// one line since the poll before.
func BenchmarkPollAtTheByteBound(b *testing.B) {
	r := listRoute(b)
	content := boundList(b, r)
	last := len(content) - 1
	for last > 0 && content[last-1] != '\n' {
		last--
	}
	before := reaction.Snapshot{}.Next(r, content[:last], clock0, poll)
	if before.State() == reaction.Unknown {
		b.Fatalf("the list before the last line: %s %s", before.Cause(), before.Detail())
	}
	samples := make([]time.Duration, 0, 64)
	for b.Loop() {
		start := time.Now()
		s := before.Next(r, content, clock0, poll)
		samples = append(samples, time.Since(start))
		if s.State() == reaction.Unknown || s.Usage().Bytes != int64(len(content)) {
			b.Fatalf("%s %s, %d bytes", s.Cause(), s.Detail(), s.Usage().Bytes)
		}
	}
	slices.Sort(samples)
	at := func(p float64) float64 { return float64(samples[int(p*float64(len(samples)-1))].Nanoseconds()) }
	b.ReportMetric(at(0.50), "p50-ns/op")
	b.ReportMetric(at(0.99), "p99-ns/op")
	b.ReportMetric(float64(len(content)), "list-bytes")
}
