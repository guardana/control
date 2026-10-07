package runview_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/runview"
	"github.com/guardana/control/internal/supervise"
)

// searches is a run of n allowed calls a second apart.
func searches(t *testing.T, n int) (*supervise.Procedure, *supervise.Result) {
	t.Helper()
	calls := make([]call, n)
	for i := range calls {
		calls[i] = call{req: fmt.Sprintf("s%04d", i), tool: "search_docs", upstream: "docs", at: time.Duration(i) * time.Second}
	}
	return judge(t, refund01, supervise.Input{Exports: []supervise.Export{export(calls...)}})
}

// TestTheViewOfEveryCallStopsAtTheBoundAndSaysSo: a run of MaxCalls calls
// is drawn whole; one more call is drawn to MaxCalls and the page says how
// many it left out.
func TestTheViewOfEveryCallStopsAtTheBoundAndSaysSo(t *testing.T) {
	const short = "stopped short"
	for _, c := range []struct {
		n     int
		short bool
	}{{runview.MaxCalls, false}, {runview.MaxCalls + 1, true}} {
		p, res := searches(t, c.n)
		raw, err := runview.Page(p, res, runview.Options{All: true})
		if err != nil {
			t.Fatal(err)
		}
		page := string(raw)
		got := marks(page)
		calls := len(got)
		if calls > 0 && strings.HasPrefix(got[len(got)-1], "gap: ") {
			calls--
		}
		if calls != runview.MaxCalls {
			t.Errorf("%d calls: %d drawn, want %d", c.n, calls, runview.MaxCalls)
		}
		if strings.Contains(page, short) != c.short {
			t.Errorf("%d calls: says it %s %v, want %v", c.n, short, !c.short, c.short)
		}
		if c.short && got[len(got)-1] != "gap: 1 call not drawn: the page stops at 1000 calls" {
			t.Errorf("%d calls: the last row is %q", c.n, got[len(got)-1])
		}
	}
}
