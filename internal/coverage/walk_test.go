package coverage_test

import (
	"fmt"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/coverage"
)

// walkBound is the bound the reference page prints.
const walkBound = 1 << 16

const cutWhy = "the walk down its trace passed its bound of 65536 spans"

func TestTheWalkBoundIsThePagesBound(t *testing.T) {
	if coverage.MaxWalkSpans != walkBound {
		t.Fatalf("MaxWalkSpans %d, the reference page prints %d", coverage.MaxWalkSpans, walkBound)
	}
}

func spanAt(i int) string { return fmt.Sprintf("%016x", i+1) }

// chain is spans 1 to n of traceA, each the child of the one before, each an
// observation named name.
func chain(n int, name string) []*observev1.Record {
	records := make([]*observev1.Record, 0, n)
	for i := 1; i <= n; i++ {
		records = append(records, obs{id: fmt.Sprintf("obs-chain-%d", i), name: name, trace: traceA,
			span: spanAt(i), parent: spanAt(i - 1)}.record())
	}
	return records
}

// enforcedChain maps records against plane a enforcing the path, its whole
// export holding one proposal at spanAt(deepest) of traceA.
func enforcedChain(t *testing.T, deepest int, records []*observev1.Record) coverage.PathCoverage {
	t.Helper()
	return mustMap(t, coverage.Input{
		Inventory: toolInventory(t),
		Planes: []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)),
			wholeExport(t, proposal{trace: traceA, span: spanAt(deepest)}.line()))},
		Sources: []coverage.Source{liveSource(selfReported, records...)},
	}).Paths[0]
}

// TestALongChainLeavesAnotherTraceChecked: 1449 nested observations of the
// path in one trace, each joined, leave a call in another trace a call
// around the plane.
func TestALongChainLeavesAnotherTraceChecked(t *testing.T) {
	const depth = 1449
	records := append(chain(depth, "create_issue"), obs{id: "obs-around", trace: traceB, span: spanAt(5)}.record())
	p := enforcedChain(t, depth, records)
	if len(p.Joins) != depth+1 {
		t.Fatalf("%d joins, want %d", len(p.Joins), depth+1)
	}
	for _, j := range p.Joins[:depth] {
		if j.Join != coverage.Joined {
			t.Fatalf("%+v, want joined", j)
		}
	}
	if last := p.Joins[depth]; last.Join != coverage.JoinAround || last.ObservationID != "obs-around" {
		t.Fatalf("%+v, want obs-around a call around the plane", last)
	}
}

// TestAWalkIsBoundedOnItsOwn: on a chain of walkBound+1 spans whose deepest
// holds the only proposal, an observation at its top has one span too many
// below it and is not checked, while one at the next span, walkBound spans,
// joins, and a call in another trace is still a call around the plane.
func TestAWalkIsBoundedOnItsOwn(t *testing.T) {
	deepest := walkBound + 1
	records := append(chain(deepest, "other_tool"),
		obs{id: "obs-top", trace: traceA, span: spanAt(1)}.record(),
		obs{id: "obs-next", trace: traceA, span: spanAt(2)}.record(),
		obs{id: "obs-around", trace: traceB, span: spanAt(1)}.record())
	p := enforcedChain(t, deepest, records)
	want := []coverage.JoinCheck{
		{ObservationID: "obs-top", Join: coverage.JoinNotChecked, Why: cutWhy, Cut: true},
		{ObservationID: "obs-next", Join: coverage.Joined},
		{ObservationID: "obs-around", Join: coverage.JoinAround},
	}
	if len(p.Joins) != len(want) {
		t.Fatalf("joins %+v, want %+v", p.Joins, want)
	}
	for i := range want {
		if p.Joins[i] != want[i] {
			t.Errorf("join %d: %+v, want %+v", i, p.Joins[i], want[i])
		}
	}
}

// TestACutWalkExitsOne: an agent can cut a walk on purpose by nesting spans
// below its call, so a cut walk is a gap in the map. The same chain with the
// observation one span lower is covered.
func TestACutWalkExitsOne(t *testing.T) {
	deepest := walkBound + 1
	base := chain(deepest, "other_tool")
	at := func(span int) *coverage.Report {
		records := append(append([]*observev1.Record(nil), base...), obs{id: "obs-a", trace: traceA, span: spanAt(span)}.record())
		return mustMap(t, coverage.Input{
			Inventory: toolInventory(t),
			Planes: []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)),
				wholeExport(t, proposal{trace: traceA, span: spanAt(deepest)}.line()))},
			Sources: []coverage.Source{liveSource(selfReported, records...)},
		})
	}
	cut := at(1)
	if j := cut.Paths[0].Joins; len(j) != 1 || !j[0].Cut || j[0].Join != coverage.JoinNotChecked {
		t.Fatalf("joins %+v, want one cut and not checked", j)
	}
	if got := coverage.ExitStatus(cut, nil); got != coverage.ExitGaps {
		t.Errorf("a cut walk: exit %d, want %d", got, coverage.ExitGaps)
	}
	if got := coverage.ExitStatus(at(2), nil); got != coverage.ExitCovered {
		t.Errorf("a walk at the bound: exit %d, want %d", got, coverage.ExitCovered)
	}
}

// TestALongChainIsWalkedOnce: every span of a chain of walkBound spans is an
// observation of the path, joined by the one proposal at its deepest. A walk
// per observation that learns nothing from the one before takes walkBound
// squared over two steps, past two billion, which no machine takes within the
// limit; a chain walked once takes a fraction of a second.
func TestALongChainIsWalkedOnce(t *testing.T) {
	const limit = 20 * time.Second
	in := coverage.Input{
		Inventory: toolInventory(t),
		Planes: []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)),
			wholeExport(t, proposal{trace: traceA, span: spanAt(walkBound)}.line()))},
		Sources: []coverage.Source{liveSource(selfReported, chain(walkBound, "create_issue")...)},
		Now:     now,
	}
	type result struct {
		r   *coverage.Report
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := coverage.Map(in)
		done <- result{r, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Map: %v", got.err)
		}
		joins := got.r.Paths[0].Joins
		if len(joins) != walkBound {
			t.Fatalf("%d joins, want %d", len(joins), walkBound)
		}
		for _, j := range joins {
			if j.Join != coverage.Joined {
				t.Fatalf("%+v, want joined", j)
			}
		}
	case <-time.After(limit):
		t.Fatalf("a chain of %d observations took longer than %v", walkBound, limit)
	}
}

// TestSpansWhoseParentsLoopStillJoin: spans whose parents loop each reach
// every other, whichever of them a walk entered first, and a span claimed
// under two parents is below each.
func TestSpansWhoseParentsLoopStillJoin(t *testing.T) {
	looped := []*observev1.Record{
		obs{id: "obs-a", trace: traceA, span: spanA, parent: spanC}.record(),
		obs{id: "obs-b", trace: traceA, span: spanB, parent: spanA}.record(),
		obs{id: "obs-c", trace: traceA, span: spanC, parent: spanB}.record(),
	}
	twoParents := []*observev1.Record{
		obs{id: "obs-a", trace: traceA, span: spanA}.record(),
		obs{id: "obs-b", trace: traceA, span: spanB}.record(),
		obs{id: "obs-c", name: "other_tool", trace: traceA, span: spanC, parent: spanA}.record(),
		obs{id: "obs-c2", name: "other_tool", trace: traceA, span: spanC, parent: spanB}.record(),
	}
	cases := []struct {
		name    string
		records []*observev1.Record
		at      string
	}{
		{"a loop, the proposal at its first span", looped, spanA},
		{"a loop, the proposal at its second span", looped, spanB},
		{"a loop, the proposal at its third span", looped, spanC},
		{"a span under two parents", twoParents, spanC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustMap(t, coverage.Input{
				Inventory: toolInventory(t),
				Planes: []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)),
					wholeExport(t, proposal{trace: traceA, span: tc.at}.line()))},
				Sources: []coverage.Source{liveSource(selfReported, tc.records...)},
			}).Paths[0]
			if len(p.Joins) < 2 {
				t.Fatalf("joins %+v, want one per observation of the path", p.Joins)
			}
			for _, j := range p.Joins {
				if j.Join != coverage.Joined {
					t.Errorf("%+v, want joined", j)
				}
			}
		})
	}
}

// TestAWalkKeepsEachPathsVerdict: two paths seen by one source at one span,
// a proposal below it for one tool only. What the walk learned of the trace
// is shared; what it found for one path is not the other's.
func TestAWalkKeepsEachPathsVerdict(t *testing.T) {
	inv := mustInventory(t, `{"schema_version":"0.1","paths":[`+
		`{"id":"gh-create","kind":"mcp_tool","upstream":"github","tool":"create_issue","sources":[{"source_id":"s1","name":"create_issue"}]},`+
		`{"id":"gh-close","kind":"mcp_tool","upstream":"github","tool":"close_issue","sources":[{"source_id":"s1","name":"close_issue"}]}]}`)
	closing := coverage.Override{Upstream: "github", Tool: "close_issue", Fingerprint: "fp2", Effect: effectWrite}
	r := mustMap(t, coverage.Input{
		Inventory: inv,
		Planes: []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite), closing),
			wholeExport(t, proposal{trace: traceA, span: spanB}.line()))},
		Sources: []coverage.Source{liveSource(selfReported, observedA.record(),
			obs{id: "obs-close", name: "close_issue", trace: traceA, span: spanA}.record(),
			obs{id: "obs-child", kind: observev1.SubjectKind_SUBJECT_KIND_MODEL, name: "m", trace: traceA, span: spanB, parent: spanA}.record())},
	})
	got := [][]coverage.JoinCheck{r.Paths[0].Joins, r.Paths[1].Joins}
	if len(got[0]) != 1 || got[0][0].Join != coverage.Joined || len(got[1]) != 1 || got[1][0].Join != coverage.JoinAround {
		t.Fatalf("joins %+v, want create_issue joined and close_issue a call around the plane", got)
	}
}
