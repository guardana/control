package metrics

import (
	"testing"

	"github.com/guardana/control/internal/gateway"
)

// TestRunRefusalsAreWrittenByCauseOnce: each counted cause is one sample of
// its own, a cause counted zero times writes none, and a key outside the
// fixed set is summed under other rather than written as a label.
func TestRunRefusalsAreWrittenByCauseOnce(t *testing.T) {
	var r Reading
	r.Adapter.RunsRefusedAtMessage = map[gateway.RunCause]uint64{
		gateway.RunMissing: 1, gateway.RunClosed: 2, gateway.RunExpired: 0, "run-1234": 4, "another": 5,
	}
	fam := family(t, parse(t, r), Prefix()+"adapter_runs_refused_at_message_total")
	want := map[string]string{"missing": "1", "closed": "2", Other: "9"}
	if len(fam.Samples) != len(want) {
		t.Fatalf("samples = %+v, want %v", fam.Samples, want)
	}
	for cause, raw := range want {
		if s, ok := fam.One(map[string]string{"cause": cause}); !ok || s.Raw != raw {
			t.Errorf("cause %s: %+v, want %s", cause, s, raw)
		}
	}
}
