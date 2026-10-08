package supervise

import (
	"maps"
	"slices"
	"sort"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// MaxRetryPairs bounds the pairs of a denial and a call, or a report, one
// retry rule compares. A denial whose pairs no longer fit is not judged: it
// gives each run that may have retried it an indeterminate finding, and the
// rule goes on with the next denial.
const MaxRetryPairs = 1 << 20

// retryGroup is the calls one plane form compares a denial with, those of
// one tool or on one resource, in the order their proposals were read. A
// call's key is what makes it no retry of a denial of the same key: its
// arguments hash, or its tool. A denial with no key skips no call.
type retryGroup struct {
	calls []*request
	keys  []string
	place map[*request]int
	// next is, for each place, the next place of another key, so a scan
	// steps over a run of one key at once; byKey is each key's places.
	next  []int
	byKey map[string][]int
	// runKeys holds up to two keys of each run's calls, and count how many
	// calls each run made.
	runKeys map[string][]string
	count   map[string]int
}

func newRetryGroup(calls []*request, keyOf func(*request) string, pos map[*controlv1.Event]int) *retryGroup {
	slices.SortStableFunc(calls, func(a, b *request) int { return pos[a.proposal] - pos[b.proposal] })
	g := &retryGroup{calls: calls, keys: make([]string, len(calls)), next: make([]int, len(calls)), place: map[*request]int{},
		byKey: map[string][]int{}, runKeys: map[string][]string{}, count: map[string]int{}}
	for i, rq := range calls {
		k, run := keyOf(rq), rq.proposal.GetRunId()
		g.keys[i], g.place[rq] = k, i
		g.byKey[k] = append(g.byKey[k], i)
		g.count[run]++
		if ks := g.runKeys[run]; len(ks) < 2 && !slices.Contains(ks, k) {
			g.runKeys[run] = append(ks, k)
		}
	}
	for i := len(calls) - 1; i >= 0; i-- {
		switch {
		case i == len(calls)-1:
			g.next[i] = len(calls)
		case g.keys[i+1] != g.keys[i]:
			g.next[i] = i + 1
		default:
			g.next[i] = g.next[i+1]
		}
	}
	return g
}

// scan visits each call of g from start on that may retry d, one of another
// key than d's, after taking their number from budget. It visits none and
// returns false when there are more than budget holds.
func (g *retryGroup) scan(d *request, key string, start int, budget *uint64, visit func(*request)) bool {
	var same uint64
	if key == "" {
		if i, in := g.place[d]; in && i >= start {
			same = 1
		}
	} else {
		at := g.byKey[key]
		same = uint64(len(at[sort.SearchInts(at, start):]))
	}
	cost := uint64(len(g.calls[start:])) - same
	if cost > *budget {
		return false
	}
	*budget -= cost
	for i := start; i < len(g.calls); {
		switch {
		case key != "" && g.keys[i] == key:
			i = g.next[i]
			continue
		case g.calls[i] != d:
			visit(g.calls[i])
		}
		i++
	}
	return true
}

// mayRetry is the runs with a call in g that a scan from the start would
// visit for d: those that may have retried it.
func (g *retryGroup) mayRetry(d *request, key string) []string {
	var out []string
	for _, run := range slices.Sorted(maps.Keys(g.count)) {
		ks, n := g.runKeys[run], g.count[run]
		if run == d.proposal.GetRunId() && key == "" {
			n--
		}
		if key == "" && n > 0 || len(ks) == 2 || len(ks) == 1 && ks[0] != key {
			out = append(out, run)
		}
	}
	return out
}

// startOf is where in g a call proposed after d was decided may first
// stand. With one export the order it was read in is its sequence, so the
// calls before d's decision are no retry; with more, any may be.
func (e *evaluation) startOf(g *retryGroup, d *request) int {
	if len(e.in.Exports) != 1 {
		return 0
	}
	pos := e.retryIx().pos
	return sort.Search(len(g.calls), func(i int) bool { return pos[g.calls[i].proposal] > pos[d.decided] })
}

// unjudgedDrafts is one indeterminate finding of rule for each run that may
// have retried d, which the bound left uncompared.
func unjudgedDrafts(rule string, d *request, runs []string) []draft {
	out := make([]draft, 0, len(runs))
	for _, run := range runs {
		dr := draft{rule: rule, anchor: []string{d.id}, cap: indeterminate, refs: []ref{d.ref(d.decided)}}
		dr.named(run)
		out = append(out, dr)
	}
	return out
}

// ownScope reports whether a call by run acts as the denied run, deniedRun:
// it is that run or one of its descendants. Any other run's retry needs
// another run's denial, so it suggests at most.
func (e *evaluation) ownScope(deniedRun, run string) bool {
	key := [2]string{deniedRun, run}
	if own, ok := e.scopes[key]; ok {
		return own
	}
	if e.parents == nil {
		e.parents = map[string]string{}
		for _, r := range treeOf(e.in) {
			e.parents[r.ID] = r.Parent
		}
	}
	own := false
	for at := run; at != "" && !own; at = e.parents[at] {
		own = at == deniedRun
	}
	e.scopes[key] = own
	return own
}
