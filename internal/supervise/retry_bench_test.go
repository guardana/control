package supervise_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/guardana/control/internal/supervise"
)

// floodTree is the root and 15 children of it.
func floodTree() []supervise.Run {
	tree := []supervise.Run{{ID: runID, Tenant: tenant}}
	for i := 1; i < 16; i++ {
		tree = append(tree, supervise.Run{ID: fmt.Sprintf("run-%032x", i), Tenant: tenant, Parent: runID})
	}
	return tree
}

// alternating is n denials on order 42, each by the next run of tree in
// turn, of the refund and of the refund by hand by turns, each with its own
// arguments: every denial is retried in both forms by every run after it.
func alternating(n int, tree []supervise.Run) supervise.Export {
	var as []act
	for i := range n {
		as = append(as, act{call: call{req: fmt.Sprintf("d%06d", i), tool: []string{"issue_refund", "refund_manual"}[i%2],
			upstream: "pay", at: 2 * time.Duration(i) * time.Millisecond, outcome: "deny", run: tree[i%len(tree)].ID},
			res: orderNo("42"), hash: "sha256:" + fmt.Sprintf("%064x", i), effect: transact})
	}
	return acts(as...)
}

// BenchmarkRetriesOfManyDenialsByManyRuns judges 2 000 and 8 000 such
// denials by 16 runs, a finding per denial and run in each plane form; work
// that grows with the denials times the runs keeps the second near four
// times the first.
func BenchmarkRetriesOfManyDenialsByManyRuns(b *testing.B) {
	raw, err := os.ReadFile("testdata/procedure-0.2.json")
	if err != nil {
		b.Fatal(err)
	}
	p, err := supervise.ReadProcedure(raw)
	if err != nil {
		b.Fatal(err)
	}
	tree := floodTree()
	for _, n := range []int{2000, 8000} {
		in := supervise.Input{Procedure: p, Run: tree[0], Tree: tree, Exports: []supervise.Export{alternating(n, tree)}}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for b.Loop() {
				res, err := supervise.Evaluate(in)
				if err != nil || len(only(res, ruleArgs)) == 0 || len(only(res, ruleRes)) == 0 {
					b.Fatalf("Evaluate: %v", err)
				}
			}
			b.ReportMetric(float64(n), "denials/op")
		})
	}
}
