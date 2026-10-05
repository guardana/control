package coverage_test

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/coverage"
)

// benchJoins is the observations of one path and the proposals of its
// plane's export, each observation joined by one proposal.
const benchJoins = 100_000

// BenchmarkJoinManyAgainstMany maps benchJoins observations against an export
// of benchJoins proposals; a scan of every proposal per observation is
// benchJoins squared.
func BenchmarkJoinManyAgainstMany(b *testing.B) {
	var buf bytes.Buffer
	records := make([]*observev1.Record, 0, benchJoins)
	buf.WriteString(header(plainQuery) + "\n" + windowEvent(-time.Hour) + "\n" + windowEvent(0) + "\n")
	for i := range benchJoins {
		trace, span := fmt.Sprintf("%032x", i+1), fmt.Sprintf("%016x", i+1)
		buf.WriteString(proposal{trace: trace, span: span}.line() + "\n")
		records = append(records, obs{id: fmt.Sprintf("obs-%d", i), trace: trace, span: span}.record())
	}
	buf.WriteString(trailer(benchJoins+2, 0, 0, true) + "\n")
	x, err := coverage.ReadExport(&buf)
	if err != nil {
		b.Fatal(err)
	}
	in := coverage.Input{Inventory: toolInventory(b), Now: now,
		Planes:  []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)), x)},
		Sources: []coverage.Source{liveSource(selfReported, records...)}}
	samples := make([]time.Duration, 0, 8)
	for b.Loop() {
		start := time.Now()
		r, err := coverage.Map(in)
		samples = append(samples, time.Since(start))
		if err != nil || len(r.Paths) != 1 || len(r.Paths[0].Joins) != benchJoins {
			b.Fatalf("Map: %v", err)
		}
		for _, j := range r.Paths[0].Joins {
			if j.Join != coverage.Joined {
				b.Fatalf("%+v, want joined", j)
			}
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	at := func(p float64) float64 { return float64(samples[int(p*float64(len(samples)-1))].Nanoseconds()) }
	b.ReportMetric(at(0.50), "p50-ns/op")
	b.ReportMetric(at(0.99), "p99-ns/op")
}
