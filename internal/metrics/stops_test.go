package metrics

import (
	"maps"
	"slices"
	"testing"

	"github.com/guardana/control/internal/metrics/metricstest"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// TestStopPollFailuresAreWrittenByCause: a cause the file's reader names and
// one the judge names are each a sample of their own, a cause the reader does
// not declare is summed under other, and the active entries are a gauge of
// their own.
func TestStopPollFailuresAreWrittenByCause(t *testing.T) {
	if slices.Contains(stoplist.Causes(), reaction.Cause(Other)) {
		t.Fatalf("%q is a stop cause, so it cannot stand for none", Other)
	}
	var r Reading
	r.Stops = stoplist.PollStats{
		Polls:  9,
		Failed: map[reaction.Cause]uint64{stoplist.CauseMissing: 2, reaction.CauseRewritten: 3, "made up": 4, "\xff": 1},
		Active: 5,
	}
	families := parse(t, r)
	f := family(t, families, Prefix()+"stops_poll_failures_total")
	want := []metricstest.Sample{
		{Labels: map[string]string{"cause": "missing"}, Raw: "2"},
		{Labels: map[string]string{"cause": "rewritten"}, Raw: "3"},
		{Labels: map[string]string{"cause": Other}, Raw: "5"},
	}
	if len(f.Samples) != len(want) {
		t.Fatalf("%+v, want %+v", f.Samples, want)
	}
	for i, s := range f.Samples {
		if !maps.Equal(s.Labels, want[i].Labels) || s.Raw != want[i].Raw {
			t.Errorf("sample %d is %v %s, want %v %s", i, s.Labels, s.Raw, want[i].Labels, want[i].Raw)
		}
	}
	for name, raw := range map[string]string{"stops_polls_total": "9", "stops_active_entries": "5"} {
		if s, ok := family(t, families, Prefix()+name).One(map[string]string{}); !ok || s.Raw != raw {
			t.Errorf("%s = %+v, want %s", name, s, raw)
		}
	}
}
