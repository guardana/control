package supervise_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/findinglog"
	"github.com/guardana/control/internal/supervise"
	"github.com/guardana/control/pkg/contract"
)

// countVerdicts is how many findings of rule res holds at each verdict, as
// its name says it.
func countVerdicts(res *supervise.Result, rule string) map[string]int {
	out := map[string]int{}
	for _, f := range only(res, rule) {
		out[f.GetFinding().GetVerdict().String()]++
	}
	return out
}

// TestManyDenialsOfOneCallAreStillRetried: n denials of one refund, each
// with the same arguments, then the refund with other arguments and the
// order refunded by another tool. Each denial is retried in both forms, by
// the root, confirmed when the root was denied and suggested when its child
// was; no denial is left unjudged, however many there are.
func TestManyDenialsOfOneCallAreStillRetried(t *testing.T) {
	for _, n := range []int{1025, 4096} {
		for _, deniedIn := range []string{runID, childRun} {
			t.Run(fmt.Sprint(n, " in ", deniedIn), func(t *testing.T) {
				var as []act
				for i := range n {
					d := denial()
					d.req, d.run, d.at = fmt.Sprintf("d%04d", i), deniedIn, time.Duration(i)*time.Millisecond
					as = append(as, d)
				}
				retry, other := again(), manual()
				retry.at, other.at = 30*time.Second, 40*time.Second
				res := supervise02(t, siblings(), acts(append(as, retry, other)...))
				want := map[string]int{"FINDING_VERDICT_CONFIRMED": n}
				if deniedIn != runID {
					want = map[string]int{"FINDING_VERDICT_SUSPECTED": n}
				}
				for _, rule := range []string{ruleArgs, ruleRes} {
					if got := countVerdicts(res, rule); rules(res)[rule] != checked || fmt.Sprint(got) != fmt.Sprint(want) {
						t.Errorf("%s is %s with %v; want %v", rule, rules(res)[rule], got, want)
					}
					for _, f := range only(res, rule) {
						if f.GetFinding().GetRunId() != runID {
							t.Fatalf("%s names %s", rule, f.GetFinding().GetRunId())
						}
					}
				}
			})
		}
	}
}

// TestRetryPairsAreBounded: n denials of one tool, each with its own
// arguments, are n(n-1)/2 pairs of a denial and a later call. 1448 are
// 1 047 628, within MaxRetryPairs, and each but the last is retried. 1449
// are 1 049 076: denial k compares 1448-k calls, so after the first 1416
// the 28 pairs left judge only the denial of 28 calls, d1420, and the last,
// of none; the 31 others give the root an indeterminate finding each, rather
// than a rule turned off.
func TestRetryPairsAreBounded(t *testing.T) {
	if supervise.MaxRetryPairs != 1<<20 {
		t.Fatalf("MaxRetryPairs %d: the runs below are sized for 1<<20", supervise.MaxRetryPairs)
	}
	for _, c := range []struct {
		n                   int
		confirmed, unjudged int
	}{{1448, 1447, 0}, {1449, 1417, 31}} {
		var as []act
		for i := range c.n {
			as = append(as, act{call: call{req: fmt.Sprintf("d%04d", i), tool: "issue_refund", upstream: "pay",
				at: time.Duration(i) * time.Second, outcome: "deny"}, hash: "sha256:" + fmt.Sprintf("%064x", i)})
		}
		res := retrySupervise(t, source(), acts(as...))
		got := countVerdicts(res, ruleArgs)
		if rules(res)[ruleArgs] != checked || got["FINDING_VERDICT_CONFIRMED"] != c.confirmed ||
			got["FINDING_VERDICT_INDETERMINATE"] != c.unjudged || len(only(res, ruleArgs)) != c.confirmed+c.unjudged {
			t.Fatalf("at %d: %s with %v", c.n, rules(res)[ruleArgs], got)
		}
		if c.n == 1449 {
			byID := map[string]string{}
			for _, f := range only(res, ruleArgs) {
				byID[f.GetFinding().GetFindingId()] = f.GetFinding().GetVerdict().String() + " " + citedEvents(f)
			}
			for req, w := range map[string]string{"d1415": "FINDING_VERDICT_CONFIRMED", "d1416": "FINDING_VERDICT_INDETERMINATE d1416-e2",
				"d1420": "FINDING_VERDICT_CONFIRMED", "d1447": "FINDING_VERDICT_INDETERMINATE d1447-e2"} {
				if g := byID[id02(ruleArgs, req, runID)]; !strings.HasPrefix(g, w) {
					t.Errorf("the finding on %s is %q, want %q", req, g, w)
				}
			}
		}
	}
}

// TestEachRetryFormIsBoundedOnItsOwn: 1024 reports of the refund and n
// denials of it on one order, each with its own arguments. The report around
// each denial is 1024 pairs: at 1024 denials exactly MaxRetryPairs, all
// judged; at 1025 the last denial is not, and is indeterminate. Within the
// same input the plane forms stay judged: other arguments are n(n-1)/2
// pairs, and one tool on one order is no retry of itself.
func TestEachRetryFormIsBoundedOnItsOwn(t *testing.T) {
	var reports []ob
	for i := range 1024 {
		reports = append(reports, ob{id: fmt.Sprintf("obs-%04d", i), name: "issue_refund", at: 5000 * time.Second})
	}
	src := source(reports...)
	for _, n := range []int{1024, 1025} {
		var as []act
		for i := range n {
			as = append(as, act{call: call{req: fmt.Sprintf("d%04d", i), tool: "issue_refund", upstream: "pay",
				at: time.Duration(i) * time.Second, outcome: "deny"}, hash: "sha256:" + fmt.Sprintf("%064x", i), res: orderNo("42")})
		}
		res := retrySupervise(t, src, acts(as...))
		want := map[string]map[string]int{
			ruleArgs:   {"FINDING_VERDICT_CONFIRMED": n - 1},
			ruleRes:    {},
			ruleAround: {"FINDING_VERDICT_SUSPECTED": 1024},
		}
		if n == 1025 {
			want[ruleAround]["FINDING_VERDICT_INDETERMINATE"] = 1
		}
		for _, rule := range retryRules {
			got := countVerdicts(res, rule)
			if rules(res)[rule] != checked || fmt.Sprint(got) != fmt.Sprint(want[rule]) {
				t.Errorf("at %d: %s is %s with %v; want %v", n, rule, rules(res)[rule], got, want[rule])
			}
		}
		if around := only(res, ruleAround); n == 1025 && len(around) == n {
			last := around[n-1]
			if last.GetFinding().GetFindingId() != id02(ruleAround, "d1024", runID) || citedEvents(last) != "d1024-e2" {
				t.Errorf("the denial left unjudged gives %s citing %q", last.GetFinding().GetFindingId(), citedEvents(last))
			}
		}
	}
}

// TestAReportAtTheTreeBoundIsWritten: a tree of MaxTreeRuns open runs, with
// the tenant, project, procedure id and version at contract.MaxStringBytes
// of a byte JSON escapes in two, makes a report the findings log writes
// within seven eighths of its line.
func TestAReportAtTheTreeBoundIsWritten(t *testing.T) {
	long := strings.Repeat(`"`, contract.MaxStringBytes)
	doc := v02With(t, `"procedure_id": "refund"`, `"procedure_id": "`+strings.Repeat(`\"`, contract.MaxStringBytes)+`"`,
		`"version": "2"`, `"version": "`+strings.Repeat(`\\`, contract.MaxStringBytes)+`"`)
	p, err := supervise.ReadProcedure([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	tree := []supervise.Run{{ID: runID, Tenant: long}}
	for i := 1; i < supervise.MaxTreeRuns; i++ {
		tree = append(tree, supervise.Run{ID: fmt.Sprintf("run-%032x", i), Tenant: long, Parent: tree[i-1].ID})
	}
	lookup := call{req: "r1", tool: "get_order", upstream: "shop", tenant: long, proj: long}
	res, err := supervise.Evaluate(supervise.Input{Procedure: p, Run: tree[0], Tree: tree, Exports: []supervise.Export{export(lookup)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Report.GetRunTree()) != supervise.MaxTreeRuns || res.Report.GetProjectId() != long {
		t.Fatalf("the report names %d runs", len(res.Report.GetRunTree()))
	}
	line, err := findinglog.Line(&findingv1alpha1.Record{Record: &findingv1alpha1.Record_SuperviseReport{SuperviseReport: res.Report}})
	if err != nil {
		t.Fatalf("the findings log refuses the report: %v", err)
	}
	t.Logf("the report takes %d bytes of %d", len(line), findinglog.MaxLineBytes)
	if len(line) > findinglog.MaxLineBytes*7/8 {
		t.Fatalf("a report of %d bytes, more than seven eighths of %d", len(line), findinglog.MaxLineBytes)
	}
}
