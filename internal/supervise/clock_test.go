package supervise_test

import (
	"slices"
	"testing"
	"time"

	"github.com/guardana/control/internal/supervise"
)

// TestOneExportIsNoOneClock: two planes of one project may send to one
// collector, so one export can hold a denial set by one plane's clock and a
// call set by another's, and nothing in an event names its plane. A call
// stamped after the denial may have been proposed before it, so the order
// of two requests read from one export only suggests a retry.
func TestOneExportIsNoOneClock(t *testing.T) {
	early := again()
	early.at = 20 * time.Second
	elsewhere := manual()
	elsewhere.at = 20 * time.Second
	for name, c := range map[string]struct {
		x    supervise.Export
		rule string
	}{
		"other arguments": {acts(denial(), early), ruleArgs},
		"another tool":    {acts(denial(), elsewhere), ruleRes},
	} {
		t.Run(name, func(t *testing.T) {
			res := retrySupervise(t, source(), c.x)
			if got, want := verdicts(res, c.rule), []string{runID + " FINDING_VERDICT_SUSPECTED"}; !slices.Equal(got, want) {
				t.Fatalf("%s findings %q, want %q:\n%s", c.rule, got, want, dump(res.Findings))
			}
		})
	}
}
