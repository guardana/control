package supervise_test

import (
	"fmt"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/supervise"
)

func spanAt(i int) string { return fmt.Sprintf("%016x", i+1) }

// chain is s1 with n observations on one span chain: top, the root at
// spanAt(0), then n-1 more each a child of the one before, of kind below.
// The deepest span is spanAt(n-1).
func chain(n int, top ob, below observev1.SubjectKind, name string) supervise.Source {
	top.span = spanAt(0)
	src := source(top)
	for i := 1; i < n; i++ {
		o := ob{id: fmt.Sprintf("obs-%d", i), name: name, kind: below, span: spanAt(i), parent: spanAt(i - 1)}
		src.Observations = append(src.Observations, o.obs())
	}
	return src
}

// decoys is 63 calls of tools no observation names, proposed at span, then
// r-last of wipe_disk, proposed at last.
func decoys(span, last string) []call {
	var calls []call
	for i := range 63 {
		calls = append(calls, call{req: fmt.Sprintf("d%02d", i), tool: fmt.Sprintf("tool-%02d", i), upstream: "ops",
			at: time.Duration(i) * time.Second, span: span})
	}
	return append(calls, call{req: "r-last", tool: "wipe_disk", upstream: "ops", at: 63 * time.Second, span: last})
}

// On a chain of 16385 spans, each decoy walks the 16384 spans from spanAt(16383)
// up to the root once. When r-last starts there too, the walks take 64 times
// 16384 steps, MaxJoinSteps, and the root's observation joins it. When r-last
// starts one span deeper, they take one step more, the root is not reached,
// and its observation is in doubt rather than unjoined.
func TestTheJoinWalksEachSpanOncePerCallAndIsBounded(t *testing.T) {
	if supervise.MaxJoinSteps != 1<<20 {
		t.Fatalf("MaxJoinSteps %d: the chain below is sized for 1<<20", supervise.MaxJoinSteps)
	}
	p := procWith(t)
	src := chain(16385, ob{id: "obs-top", name: "wipe_disk"}, observev1.SubjectKind_SUBJECT_KIND_AGENT, "mcp_call")
	for _, last := range []int{16383, 16384} {
		res := evaluate(t, supervise.Input{Procedure: p, Sources: []supervise.Source{src},
			Exports: []supervise.Export{export(decoys(spanAt(16383), spanAt(last))...)}})
		if got := res.Report.GetRead().GetObservationsTaken(); got != 1 {
			t.Fatalf("r-last at %d: %d observations taken, want 1", last, got)
		}
		plane := res.Findings
		if last == 16384 {
			if len(plane) != 65 {
				t.Fatalf("r-last at %d: %d findings, want 65", last, len(plane))
			}
			sameFindings(t, plane[64:], want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, indetermin,
				id("STEP_OUTSIDE_PROCEDURE", "wipe_disk"), obsRef("s1", "obs-top")))
			plane = plane[:64]
		}
		if len(plane) != 64 {
			t.Fatalf("r-last at %d: %d findings, want 64", last, len(plane))
		}
		for _, f := range plane {
			if len(f.GetRefs()) != 1 || f.GetRefs()[0].GetEvent() == nil {
				t.Fatalf("r-last at %d: a finding rests on an observation: %s", last,
					dump([]*findingv1alpha1.FindingRecord{f}))
			}
		}
	}
}

func TestAChainOf10000ObservationsJoinsItsCall(t *testing.T) {
	p := procWith(t)
	src := chain(10000, ob{id: "obs-0", name: "wipe_disk"}, observev1.SubjectKind_SUBJECT_KIND_TOOL, "wipe_disk")
	res := evaluate(t, supervise.Input{Procedure: p, Exports: []supervise.Export{export(
		call{req: "r5", tool: "wipe_disk", upstream: "ops", span: spanAt(9999)})}, Sources: []supervise.Source{src}})
	if got := res.Report.GetRead().GetObservationsTaken(); got != 10000 {
		t.Fatalf("%d observations taken, want 10000", got)
	}
	sameFindings(t, res.Findings, want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, confirmed,
		id("STEP_OUTSIDE_PROCEDURE", "wipe_disk", "ops"), evRef("r5-e1", "r5")))
}

func TestASpanCycleEndsTheWalk(t *testing.T) {
	p := procWith(t)
	src := source(
		ob{id: "obs-a", name: "wipe_disk", span: "aaaaaaaaaaaaaaaa", parent: "bbbbbbbbbbbbbbbb"},
		ob{id: "obs-b", name: "mcp_call", kind: observev1.SubjectKind_SUBJECT_KIND_AGENT,
			span: "bbbbbbbbbbbbbbbb", parent: "aaaaaaaaaaaaaaaa"},
		ob{id: "obs-c", name: "drop_table", span: "cccccccccccccccc", parent: "cccccccccccccccc"})
	res := evaluate(t, supervise.Input{Procedure: p, Exports: []supervise.Export{export(
		call{req: "r5", tool: "wipe_disk", upstream: "ops", span: "bbbbbbbbbbbbbbbb"})}, Sources: []supervise.Source{src}})
	sameFindings(t, res.Findings,
		want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, confirmed, id("STEP_OUTSIDE_PROCEDURE", "wipe_disk", "ops"),
			evRef("r5-e1", "r5")),
		want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, suspected, id("STEP_OUTSIDE_PROCEDURE", "drop_table"),
			obsRef("s1", "obs-c")))
}

func BenchmarkJoinAChainOf10000Observations(b *testing.B) {
	p, err := supervise.ReadProcedure([]byte(procJSON))
	if err != nil {
		b.Fatal(err)
	}
	in := supervise.Input{Procedure: p, Run: supervise.Run{ID: runID, Tenant: tenant},
		Exports: []supervise.Export{export(call{req: "r5", tool: "wipe_disk", upstream: "ops", span: spanAt(9999)})},
		Sources: []supervise.Source{chain(10000, ob{id: "obs-0", name: "wipe_disk"},
			observev1.SubjectKind_SUBJECT_KIND_TOOL, "wipe_disk")}}
	for b.Loop() {
		res, err := supervise.Evaluate(in)
		if err != nil || len(res.Findings) != 1 {
			b.Fatalf("Evaluate: %v, %d findings, want 1", err, len(res.Findings))
		}
	}
	b.ReportMetric(10000, "observations/op")
}
