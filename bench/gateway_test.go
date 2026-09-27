// Benchmark of one MCP tools/call through the plane: an agent's client, the
// MCP adapter, the pipeline deciding against a signed bundle, the spool on a
// temporary directory, and an upstream that answers at once. The same call
// made straight to that upstream is the baseline. The agent and upstream legs
// are in-process pipes; the exporter drains the spool to a collector in the
// same process over loopback HTTP, as a running plane's exporter would.
package bench_test

import (
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/otel"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// TestBenchmarkedRoundTripSucceeds holds every arrangement the benchmark
// times to a call that worked: the upstream's answer reaches the agent, the
// spool took the four records an allowed call leaves before the answer came
// back, and the exporter delivered exactly those four. A blocked call is
// answered sooner than an executed one, so without this a plane that refused
// everything would be timed as a fast one.
func TestBenchmarkedRoundTripSucceeds(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		res, err := callLookup(directSession(t), "/a")
		if err != nil || res.IsError {
			t.Fatalf("direct call: err %v, result %+v", err, res)
		}
		if got := answerText(res); got != "found /a" {
			t.Fatalf("direct answer %q, want %q", got, "found /a")
		}
	})
	for _, mode := range fsyncModes() {
		t.Run("plane/"+mode.name, func(t *testing.T) {
			r := newRig(t, mode, true)
			res, err := callLookup(r.agent, "/b")
			// Read before anything else can run: the claim is that the
			// records were appended before the answer, not soon after it.
			events := r.recorded.events()
			if err != nil || res.IsError {
				t.Fatalf("call through the plane: err %v, result %+v", err, res)
			}
			if got := answerText(res); got != "found /b" {
				t.Fatalf("answer through the plane %q, want %q", got, "found /b")
			}
			kinds := make([]controlv1.EventKind, 0, len(events))
			for _, e := range events {
				kinds = append(kinds, e.GetKind())
			}
			want := []controlv1.EventKind{
				controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED,
				controlv1.EventKind_EVENT_KIND_POLICY_DECIDED,
				controlv1.EventKind_EVENT_KIND_ACTION_STARTED,
				controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED,
			}
			if !slices.Equal(kinds, want) {
				t.Fatalf("when the answer arrived the spool held %v, want %v", kinds, want)
			}
			if err := evidence.ValidateChain(events); err != nil {
				t.Fatalf("ValidateChain: %v", err)
			}
			if codes := events[1].GetDecision().GetReasonCodes(); !slices.Contains(codes, "RULE_ALLOW") {
				t.Fatalf("the recorded decision says %v, want RULE_ALLOW", codes)
			}
			waitAcknowledged(t, r, 4)
		})
	}
}

// waitAcknowledged fails unless the exporter acknowledges exactly n records.
// The exporter delivers on its own schedule, a linger of 100 ms among it, so
// the wait for the count is bounded; the count itself is exact.
func waitAcknowledged(t testing.TB, r *rig, n uint64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for r.exporter.Stats().Acknowledged < n && time.Now().Before(deadline) {
		if err := r.exporterFailure(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// One more linger, so a record past n would have been delivered too.
	time.Sleep(2 * otel.DefaultLinger)
	if err := r.exporterFailure(); err != nil {
		t.Fatal(err)
	}
	if got := r.exporter.Stats().Acknowledged; got != n {
		t.Fatalf("the exporter acknowledged %d records, want %d", got, n)
	}
	if r.requests.Load() == 0 {
		t.Fatal("the collector accepted no request")
	}
}

// BenchmarkGatewayRoundTrip measures one tools/call per iteration, straight
// to the upstream and through the plane under each fsync mode, and reports
// the median and 99th percentile of the per-call durations as BenchmarkDecide
// does. A call that fails or comes back blocked stops the run. Afterwards, off
// the clock, the exporter has to deliver the four records of every call: an
// exporter that fell behind or stopped would otherwise make the plane look
// cheaper than a running one.
func BenchmarkGatewayRoundTrip(b *testing.B) {
	b.Run("direct", func(b *testing.B) {
		timeRoundTrips(b, directSession(b))
	})
	for _, mode := range fsyncModes() {
		b.Run("plane/"+mode.name, func(b *testing.B) {
			r := newRig(b, mode, false)
			calls := timeRoundTrips(b, r.agent)
			b.StopTimer()
			waitAcknowledged(b, r, 4*calls)
		})
	}
}

// timeRoundTrips returns the number of calls it made.
func timeRoundTrips(b *testing.B, cs *sdk.ClientSession) uint64 {
	b.Helper()
	b.ReportAllocs()
	samples := make([]time.Duration, 0, 1<<14)
	var calls uint64
	for b.Loop() {
		calls++
		start := time.Now()
		res, err := callLookup(cs, "/bench")
		samples = append(samples, time.Since(start))
		if err != nil || res.IsError {
			b.Fatalf("call: err %v, result %+v", err, res)
		}
	}
	reportPercentiles(b, samples)
	return calls
}
