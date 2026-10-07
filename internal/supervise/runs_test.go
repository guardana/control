package supervise_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/findinglog"
	"github.com/guardana/control/internal/supervise"
	"google.golang.org/protobuf/encoding/protojson"
)

// id02 is a finding id of the 0.2 refund fixture, version 2, about the tree
// whose root is runID.
func id02(rule string, anchor ...string) string {
	return handID(append([]string{tenant, proj, runID, "refund", "2", rule, "1"}, anchor...)...)
}

// only is the findings of rule in res.
func only(res *supervise.Result, rule string) []*findingv1alpha1.FindingRecord {
	var out []*findingv1alpha1.FindingRecord
	for _, f := range res.Findings {
		if f.GetFinding().GetRuleId() == rule {
			out = append(out, f)
		}
	}
	return out
}

// names holds one finding of rule naming run with the id given.
func names(t *testing.T, res *supervise.Result, rule, run, wantID string) {
	t.Helper()
	got := only(res, rule)
	if len(got) != 1 {
		t.Fatalf("%d %s findings, want 1:\n%s", len(got), rule, dump(res.Findings))
	}
	if f := got[0].GetFinding(); f.GetRunId() != run || f.GetFindingId() != wantID {
		t.Fatalf("%s names %s with id %s; want %s with id %s", rule, f.GetRunId(), f.GetFindingId(), run, wantID)
	}
}

func deny(req, run string, at time.Duration) call {
	return call{req: req, tool: "issue_refund", upstream: "pay", at: at, outcome: "deny", run: run}
}

// TestARepeatedDenialNamesTheRunWhoseDenialCrossedTheBound: of four denials
// the fourth in time names its run, whichever run made the others, whatever
// came after it and in whatever order the exports are read.
func TestARepeatedDenialNamesTheRunWhoseDenialCrossedTheBound(t *testing.T) {
	root3 := []call{deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second), deny("d3", "", 30*time.Second)}
	for name, c := range map[string]struct {
		exports []supervise.Export
		run     string
	}{
		"the child's denial is the fourth": {[]supervise.Export{export(append(root3, deny("d4", childRun, 40*time.Second))...)}, childRun},
		"the root's denial is the fourth": {[]supervise.Export{export(deny("c1", childRun, 5*time.Second),
			deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second), deny("d3", "", 30*time.Second))}, runID},
		"the child's denial is the fifth": {[]supervise.Export{export(append(root3, deny("d4", "", 40*time.Second),
			deny("c1", childRun, 50*time.Second))...)}, runID},
		"the fourth in time is read first": {[]supervise.Export{export(deny("c1", childRun, 35*time.Second)),
			export(append(root3, deny("d4", "", 40*time.Second))...)}, childRun},
	} {
		t.Run(name, func(t *testing.T) {
			res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(), Exports: c.exports})
			names(t, res, supervise.RuleRepeatedDenial, c.run, id02("REPEATED_DENIAL", "issue_refund", "pay", c.run))
		})
	}
}

// TestARepeatedDenialWhoseCrossingIsUntoldIsIndeterminate: when the
// fourth denial shares its time with another run's, or has none, the run it
// names may not be the one that crossed the bound; a tie within one run
// changes nothing.
func TestARepeatedDenialWhoseCrossingIsUntoldIsIndeterminate(t *testing.T) {
	untimed := export(deny("c1", childRun, 40*time.Second))
	for _, ev := range untimed.Events {
		ev.OccurredAt = nil
	}
	for name, c := range map[string]struct {
		exports []supervise.Export
		verdict controlv1.FindingVerdict
	}{
		"a tie with another run": {[]supervise.Export{export(deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second),
			deny("d3", "", 30*time.Second), deny("c1", childRun, 30*time.Second))}, indetermin},
		"a tie within one run": {[]supervise.Export{export(deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second),
			deny("d3", "", 30*time.Second), deny("d4", "", 30*time.Second))}, confirmed},
		"the fourth with no time": {[]supervise.Export{export(deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second),
			deny("d3", "", 30*time.Second)), untimed}, indetermin},
		"one with no time before the fourth": {[]supervise.Export{export(deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second),
			deny("d3", "", 30*time.Second), deny("d4", "", 50*time.Second)), untimed}, indetermin},
		"told": {[]supervise.Export{export(deny("d1", "", 10*time.Second), deny("d2", "", 20*time.Second),
			deny("d3", "", 30*time.Second), deny("c1", childRun, 40*time.Second))}, confirmed},
	} {
		t.Run(name, func(t *testing.T) {
			res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(), Exports: c.exports})
			got := only(res, supervise.RuleRepeatedDenial)
			if len(got) != 1 || got[0].GetFinding().GetVerdict() != c.verdict {
				t.Fatalf("want one %s finding:\n%s", c.verdict, dump(res.Findings))
			}
		})
	}
}

// TestADeadlineWhoseCrossingIsUntoldIsIndeterminate: two runs' events at the
// first instant past the deadline leave the run that crossed it untold.
func TestADeadlineWhoseCrossingIsUntoldIsIndeterminate(t *testing.T) {
	lookup := call{req: "r1", tool: "get_order", upstream: "shop"}
	for name, c := range map[string]struct {
		other   string
		verdict controlv1.FindingVerdict
	}{
		"a tie with another run": {childRun, indetermin},
		"a tie within one run":   {"", confirmed},
	} {
		t.Run(name, func(t *testing.T) {
			x := export(lookup, call{req: "r2", tool: "search_docs", upstream: "docs", at: 700 * time.Second},
				call{req: "r3", tool: "search_docs", upstream: "docs", at: 700 * time.Second, run: c.other})
			res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(), Exports: []supervise.Export{x}})
			got := only(res, supervise.RuleDeadlineExceeded)
			if len(got) != 1 || got[0].GetFinding().GetVerdict() != c.verdict {
				t.Fatalf("want one %s finding:\n%s", c.verdict, dump(res.Findings))
			}
		})
	}
}

// TestADeadlineNamesTheRunWhoseEventCrossedIt: the first event past
// deadline_seconds names its run, and is cited, not the last event.
func TestADeadlineNamesTheRunWhoseEventCrossedIt(t *testing.T) {
	lookup := call{req: "r1", tool: "get_order", upstream: "shop"}
	for name, c := range map[string]struct {
		calls      []call
		run, cites string
	}{
		"a child's event crosses it": {[]call{lookup,
			{req: "c1", tool: "search_docs", upstream: "docs", at: 700 * time.Second, run: childRun}}, childRun, "c1-e1"},
		"the root's event crosses it before the child's": {[]call{lookup,
			{req: "r2", tool: "search_docs", upstream: "docs", at: 650 * time.Second},
			{req: "c1", tool: "search_docs", upstream: "docs", at: 700 * time.Second, run: childRun}}, runID, "r2-e1"},
	} {
		t.Run(name, func(t *testing.T) {
			res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(), Exports: []supervise.Export{export(c.calls...)}})
			names(t, res, supervise.RuleDeadlineExceeded, c.run, id02("DEADLINE_EXCEEDED", c.run))
			refs := only(res, supervise.RuleDeadlineExceeded)[0].GetRefs()
			if len(refs) != 2 || refs[0].GetEvent().GetEventId() != "r1-e1" || refs[1].GetEvent().GetEventId() != c.cites {
				t.Fatalf("cites %v; want r1-e1 and %s", refs, c.cites)
			}
		})
	}
}

// TestACallOutsideTheProcedureNamesTheRunThatMadeIt: one tool called by the
// root and by the child is two findings, each naming its run and citing its
// own call; a source's report claimed for the child names the supervised run.
func TestACallOutsideTheProcedureNamesTheRunThatMadeIt(t *testing.T) {
	calls := []call{
		{req: "r1", tool: "get_order", upstream: "shop"},
		{req: "w1", tool: "wipe_disk", upstream: "ops", at: 10 * time.Second},
		{req: "w2", tool: "wipe_disk", upstream: "ops", at: 20 * time.Second, run: childRun},
	}
	src := source(ob{id: "obs-1", name: "drop_table", run: childRun, span: "1111111111111111"})
	res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(), Exports: []supervise.Export{export(calls...)},
		Sources: []supervise.Source{src}})
	got := only(res, supervise.RuleStepOutsideProcedure)
	type named struct{ run, id, cites string }
	want := []named{
		{runID, id02("STEP_OUTSIDE_PROCEDURE", "wipe_disk", "ops", runID), "w1-e1"},
		{childRun, id02("STEP_OUTSIDE_PROCEDURE", "wipe_disk", "ops", childRun), "w2-e1"},
		{runID, id02("STEP_OUTSIDE_PROCEDURE", "drop_table"), "obs-1"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d findings, want %d:\n%s", len(got), len(want), dump(got))
	}
	for i, w := range want {
		f, refs := got[i].GetFinding(), got[i].GetRefs()
		cited := refs[0].GetEvent().GetEventId() + refs[0].GetObservation().GetObservationId()
		if f.GetRunId() != w.run || f.GetFindingId() != w.id || len(refs) != 1 || cited != w.cites {
			t.Errorf("finding %d names %s, id %s, cites %d refs from %s; want %s, %s, %s",
				i, f.GetRunId(), f.GetFindingId(), len(refs), cited, w.run, w.id, w.cites)
		}
	}
}

// TestAnAbsenceRuleNamesTheSupervisedRun: a step out of order whose instance
// a child made names the root of the tree.
func TestAnAbsenceRuleNamesTheSupervisedRun(t *testing.T) {
	calls := []call{
		{req: "r1", tool: "get_order", upstream: "shop"},
		{req: "m1", tool: "send_mail", upstream: "mail", at: 5 * time.Second, run: childRun},
		{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second},
	}
	res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(runID, childRun, grandchildRun),
		Exports: []supervise.Export{export(calls...)}})
	names(t, res, supervise.RuleStepOutOfOrder, runID, id02("STEP_OUT_OF_ORDER", "notify"))
	if run := only(res, supervise.RuleStepOutOfOrder)[0].GetRefs()[0].GetEvent().GetRunId(); run != childRun {
		t.Fatalf("the out of order call is cited with run %q, want %s", run, childRun)
	}
}

// busyTree is a closed tree whose calls fire every rule a 0.2 procedure
// applies today: denials, a deadline, calls outside the procedure, a step
// out of order and a failure followed by another step. runOf is each
// request's run.
func busyTree() ([]call, map[string]string) {
	calls := []call{
		{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"},
		{req: "m1", tool: "send_mail", upstream: "mail", at: 5 * time.Second, run: childRun},
		deny("d1", "", 10*time.Second), deny("d2", childRun, 20*time.Second),
		deny("d3", grandchildRun, 30*time.Second), deny("d4", grandchildRun, 40*time.Second),
		{req: "w1", tool: "wipe_disk", upstream: "ops", at: 50 * time.Second, run: grandchildRun},
		{req: "z1", tool: "search_docs", upstream: "docs", at: 900 * time.Second, run: childRun},
	}
	runOf := map[string]string{"r1": runID, "m1": childRun, "d1": runID, "d2": childRun,
		"d3": grandchildRun, "d4": grandchildRun, "w1": grandchildRun, "z1": childRun}
	return calls, runOf
}

// TestEveryEventOfA02RecordNamesItsRun: under 0.2 every event reference
// carries the run of its event, a record with one is schema 0.2 and one
// without is 0.1, and the findings log writes each.
func TestEveryEventOfA02RecordNamesItsRun(t *testing.T) {
	calls, runOf := busyTree()
	res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(runID, childRun, grandchildRun),
		Exports: []supervise.Export{export(calls...)}})
	rulesSeen := map[string]bool{}
	for _, f := range res.Findings {
		rulesSeen[f.GetFinding().GetRuleId()] = true
		checkRuns(t, f, runOf)
	}
	for _, rule := range []string{"REPEATED_DENIAL", "DEADLINE_EXCEEDED", "STEP_OUTSIDE_PROCEDURE", "STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE"} {
		if !rulesSeen[rule] {
			t.Errorf("no %s finding:\n%s", rule, dump(res.Findings))
		}
	}
}

// checkRuns holds f's event references to the runs runOf gives by request,
// its schema to whether it has one, and f to what the findings log writes.
func checkRuns(t *testing.T, f *findingv1alpha1.FindingRecord, runOf map[string]string) {
	t.Helper()
	events := 0
	for _, r := range f.GetRefs() {
		e := r.GetEvent()
		if e == nil {
			continue
		}
		events++
		if want := runOf[e.GetRequestId()]; e.GetRunId() != want {
			t.Errorf("%s cites %s with run %q, want %q", f.GetFinding().GetRuleId(), e.GetEventId(), e.GetRunId(), want)
		}
	}
	if want := map[bool]string{true: "0.2", false: "0.1"}[events > 0]; f.GetSchemaVersion() != want {
		t.Errorf("%s with %d events is schema %s, want %s", f.GetFinding().GetRuleId(), events, f.GetSchemaVersion(), want)
	}
	if _, err := findinglog.Line(&findingv1alpha1.Record{Record: &findingv1alpha1.Record_FindingRecord{FindingRecord: f}}); err != nil {
		t.Errorf("the log refuses %s: %v", protojson.Format(f), err)
	}
}

// TestNoEventOfA01RecordNamesItsRun: the root's calls of the busy tree under
// 0.1 fire, and no record names an event's run.
func TestNoEventOfA01RecordNamesItsRun(t *testing.T) {
	calls, _ := busyTree()
	var root []call
	for _, c := range calls {
		if c.run == "" {
			root = append(root, c)
		}
	}
	old := evaluate(t, supervise.Input{Procedure: procWith(t, `"max_denials":4`, `"max_denials":1`), Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(root...)}})
	if len(old.Findings) == 0 {
		t.Fatal("the 0.1 run fires nothing")
	}
	for _, f := range old.Findings {
		for _, r := range f.GetRefs() {
			if r.GetEvent().GetRunId() != "" || f.GetSchemaVersion() != "0.1" {
				t.Fatalf("a 0.1 finding names an event's run: %s", protojson.Format(f))
			}
		}
	}
}

// TestA02ReportListsEveryRuleItsSchemaKnows: the report of a 0.2 procedure
// names the eleven rules in the table's order, those this build does not
// apply yet as not checked with the reason.
func TestA02ReportListsEveryRuleItsSchemaKnows(t *testing.T) {
	res := treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: family(), Exports: []supervise.Export{export(conforming()...)}})
	var ids []string
	for _, r := range res.Report.GetRules() {
		ids = append(ids, r.GetRuleId())
		if r.GetRuleVersion() != "1" {
			t.Errorf("%s at version %q", r.GetRuleId(), r.GetRuleVersion())
		}
	}
	want := []string{"REPEATED_DENIAL", "STEP_OUTSIDE_PROCEDURE", "DEADLINE_EXCEEDED", "REQUIRED_STEP_SKIPPED",
		"STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE", "RESOURCE_OUTSIDE_RUN", "DENIED_ACTION_RETRIED_ARGUMENTS",
		"DENIED_ACTION_RETRIED_RESOURCE", "DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN"}
	if !slices.Equal(ids, want) {
		t.Fatalf("report rules %q, want %q", ids, want)
	}
	got := rules(res)
	for _, rule := range want[10:] {
		if got[rule] != "RULE_STATE_NOT_CHECKED:this build does not apply the rule" {
			t.Errorf("%s is %s", rule, got[rule])
		}
	}
	if got["REPEATED_DENIAL"] != checked {
		t.Errorf("REPEATED_DENIAL is %s", got["REPEATED_DENIAL"])
	}
	if old := evaluate(t, supervise.Input{Procedure: procWith(t), Exports: []supervise.Export{export(conforming()...)}}); len(old.Report.GetRules()) != 6 ||
		strings.Contains(protojson.Format(old.Report), "RESOURCE_OUTSIDE_RUN") {
		t.Fatalf("a 0.1 report lists %v", old.Report.GetRules())
	}
}
