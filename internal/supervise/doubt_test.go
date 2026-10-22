package supervise_test

import (
	"maps"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

// compliant is lookup r1 then refund r3, both completed.
func compliant() []call {
	return []call{
		{req: "r1", tool: "get_order", upstream: "shop"},
		{req: "r3", tool: "issue_refund", upstream: "pay", at: 20 * time.Second},
	}
}

// everyRuleIs is each 0.1 rule in state:why.
func everyRuleIs(state string) map[string]string {
	out := map[string]string{}
	for _, id := range []string{"REPEATED_DENIAL", "STEP_OUTSIDE_PROCEDURE", "DEADLINE_EXCEEDED",
		"REQUIRED_STEP_SKIPPED", "STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE"} {
		out[id] = state
	}
	return out
}

// TestARequestInDoubtLeavesNoRuleCheckedOnItsTrail: a closed run that keeps
// to its procedure finds nothing whether or not refund's trail is in doubt,
// so only the rules' states tell the two apart. A trail in doubt may hide a
// failure, a denial, a tool or a time, so no rule that reads it is checked.
func TestARequestInDoubtLeavesNoRuleCheckedOnItsTrail(t *testing.T) {
	p := procWith(t)
	clean := closedRun(t, p, export(compliant()...))
	sameFindings(t, clean.Findings)
	if got, want := rules(clean), everyRuleIs(checked); !maps.Equal(got, want) {
		t.Fatalf("a clean run's rules %v, want %v", got, want)
	}
	brokenLast := func(x *supervise.Export, _ call) []supervise.Export {
		x.Events[len(x.Events)-1].PrevEventId = "r3-e9"
		return nil
	}
	for name, doubt := range map[string]func(*supervise.Export, call) []supervise.Export{
		"its last link broken": brokenLast, "a broken chain": doubtOf["a broken chain"],
		"a conflicting copy": doubtOf["a conflicting copy"],
	} {
		t.Run(name, func(t *testing.T) {
			calls := compliant()
			x := export(calls...)
			res := closedRun(t, p, append([]supervise.Export{x}, doubt(&x, calls[1])...)...)
			sameFindings(t, res.Findings)
			if got, want := rules(res), everyRuleIs(`RULE_STATE_NOT_CHECKED:request "r3" is in doubt`); !maps.Equal(got, want) {
				t.Fatalf("rules %v, want %v", got, want)
			}
		})
	}
}

// TestTheRequestInDoubtNamedIsTheLeastOfThem: which one the why names does
// not follow the order the evidence was read in.
func TestTheRequestInDoubtNamedIsTheLeastOfThem(t *testing.T) {
	p := procWith(t)
	for _, order := range [][]call{compliant(), {compliant()[1], compliant()[0]}} {
		x := export(order...)
		for _, ev := range x.Events {
			if ev.GetEventId() == "r1-e4" || ev.GetEventId() == "r3-e4" {
				ev.PrevEventId = "e9"
			}
		}
		want := everyRuleIs(`RULE_STATE_NOT_CHECKED:request "r1" and 1 more are in doubt`)
		if got := rules(closedRun(t, p, x)); !maps.Equal(got, want) {
			t.Fatalf("read %s first: rules %v, want %v", order[0].req, got, want)
		}
	}
}

// TestAFindingOnATrailInDoubtStands: the run's failed refund is followed by
// notify; the finding is written, indeterminate, and its rule not checked.
func TestAFindingOnATrailInDoubtStands(t *testing.T) {
	p := procWith(t)
	x := export(call{req: "r1", tool: "get_order", upstream: "shop"},
		call{req: "r3", tool: "issue_refund", upstream: "pay", at: 20 * time.Second, outcome: "fail"},
		call{req: "r4", tool: "send_mail", upstream: "mail", at: 30 * time.Second})
	x.Events[len(x.Events)-1].PrevEventId = "r4-e9"
	res := closedRun(t, p, x)
	sameFindings(t, res.Findings, want(p, "CONTINUED_AFTER_FAILURE", critical, alert, indetermin,
		id("CONTINUED_AFTER_FAILURE", "r3"), evRef("r3-e4", "r3"), evRef("r4-e1", "r4")))
	if got := rules(res)["CONTINUED_AFTER_FAILURE"]; got != `RULE_STATE_NOT_CHECKED:request "r4" is in doubt` {
		t.Fatalf("CONTINUED_AFTER_FAILURE is %s", got)
	}
}

// TestA02RuleOnATrailInDoubtIsNotChecked: the conforming 0.2 tree with the
// refund's last link broken checks none of the eleven rules, and
// EXCEPTION_TAKEN follows the rule its exceptions waive.
func TestA02RuleOnATrailInDoubtIsNotChecked(t *testing.T) {
	customer := orderNo("42")
	customer.Type = "crm_customer"
	customer.Id = "c1"
	x := acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("42")).in(childRun),
		act{call: call{req: "r3", tool: "send_mail", upstream: "mail", at: 20 * time.Second}, res: customer})
	for _, ev := range x.Events {
		if ev.GetEventId() == "r2-e4" {
			ev.PrevEventId = "r2-e9"
		}
	}
	tree := closedFamily()
	res, err := supervise.Evaluate(supervise.Input{Procedure: inheritProc(t), Run: tree[0], Tree: tree,
		Exports: []supervise.Export{x}, Sources: []supervise.Source{source()}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	sameFindings(t, res.Findings)
	const doubt = `RULE_STATE_NOT_CHECKED:request "r2" is in doubt`
	want := everyRuleIs(doubt)
	for _, id := range []string{"RESOURCE_OUTSIDE_RUN", "DENIED_ACTION_RETRIED_ARGUMENTS",
		"DENIED_ACTION_RETRIED_RESOURCE", "DENIED_ACTION_RETRIED_AROUND"} {
		want[id] = doubt
	}
	want["EXCEPTION_TAKEN"] = `RULE_STATE_NOT_CHECKED:exceptions on STEP_OUTSIDE_PROCEDURE are not judged: request "r2" is in doubt`
	if got := rules(res); !maps.Equal(got, want) {
		t.Fatalf("rules %v, want %v", got, want)
	}
}

// TestADoubtLeavesARuleOffOrNotCheckedAsItWas: a rule the procedure turns
// off stays off, and one an open run leaves not checked keeps that reason.
func TestADoubtLeavesARuleOffOrNotCheckedAsItWas(t *testing.T) {
	const doubt = `RULE_STATE_NOT_CHECKED:request "r3" is in doubt`
	open := "RULE_STATE_NOT_CHECKED:the run is open"
	for name, c := range map[string]struct {
		p      *supervise.Procedure
		closed bool
		want   map[string]string
	}{
		"max_denials left out": {procWith(t, `"max_denials":4,`, ""), true, map[string]string{
			"REPEATED_DENIAL": "RULE_STATE_OFF:the procedure leaves max_denials out", "STEP_OUTSIDE_PROCEDURE": doubt,
			"DEADLINE_EXCEEDED": doubt, "REQUIRED_STEP_SKIPPED": doubt, "STEP_OUT_OF_ORDER": doubt,
			"CONTINUED_AFTER_FAILURE": doubt}},
		"an open run": {procWith(t), false, map[string]string{
			"REPEATED_DENIAL": doubt, "STEP_OUTSIDE_PROCEDURE": doubt, "DEADLINE_EXCEEDED": doubt,
			"REQUIRED_STEP_SKIPPED": open, "STEP_OUT_OF_ORDER": open, "CONTINUED_AFTER_FAILURE": open}},
	} {
		t.Run(name, func(t *testing.T) {
			x := export(compliant()...)
			x.Events[len(x.Events)-1].PrevEventId = "r3-e9"
			res := evaluate(t, supervise.Input{Procedure: c.p, Run: supervise.Run{Closed: c.closed}, Exports: []supervise.Export{x}})
			if got := rules(res); !maps.Equal(got, c.want) {
				t.Fatalf("rules %v, want %v", got, c.want)
			}
		})
	}
}

// TestALongIDInDoubtIsQuotedAndCut: the why quotes at most 64 bytes of the
// request id, so a producer's id cannot grow the report past its line.
func TestALongIDInDoubtIsQuotedAndCut(t *testing.T) {
	long := "r" + strings.Repeat("x", 12000)
	x := export(append(compliant(), call{req: long, tool: "send_mail", upstream: "mail", at: 30 * time.Second})...)
	want := `RULE_STATE_NOT_CHECKED:request "r` + strings.Repeat("x", 63) + `" (truncated) is in doubt`
	if got := rules(closedRun(t, procWith(t), x)); !maps.Equal(got, everyRuleIs(want)) {
		t.Fatalf("rules %v, want each %q", got, want)
	}
}

// TestAnObservationInDoubtLeavesTheRulesThatReadReportsNotChecked: obs-9 is
// read with two contents, a step's name and a name outside the procedure.
// Whichever is read first, no rule that reads a source's reports is
// checked, so neither order passes.
func TestAnObservationInDoubtLeavesTheRulesThatReadReportsNotChecked(t *testing.T) {
	p := procWith(t)
	inside := ob{id: "obs-9", name: "get_order", span: "9999999999999999", at: 5 * time.Second}
	outside := inside
	outside.name = "drop_table"
	for name, order := range map[string][]ob{"outside first": {outside, inside}, "inside first": {inside, outside}} {
		t.Run(name, func(t *testing.T) {
			res := evaluate(t, supervise.Input{Procedure: p, Run: supervise.Run{Closed: true},
				Exports: []supervise.Export{export(conforming()...)}, Sources: []supervise.Source{source(order...)}})
			want := everyRuleIs(checked)
			want["STEP_OUTSIDE_PROCEDURE"] = `RULE_STATE_NOT_CHECKED:observation "obs-9" is in doubt`
			if got := rules(res); !maps.Equal(got, want) {
				t.Fatalf("rules %v, want %v", got, want)
			}
		})
	}
}

// conflictingSources is the conforming 0.2 tree supervised with sources s1
// and s2 whose logs counted conflicts as given, and their rules.
func conflictingSources(t *testing.T, s1, s2 uint64) map[string]string {
	t.Helper()
	customer := orderNo("42")
	customer.Type = "crm_customer"
	customer.Id = "c1"
	x := acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("42")).in(childRun),
		act{call: call{req: "r3", tool: "send_mail", upstream: "mail", at: 20 * time.Second}, res: customer})
	one, two := source(), source()
	two.SourceID = "s2"
	one.Conflicts, two.Conflicts = s1, s2
	tree := closedFamily()
	res, err := supervise.Evaluate(supervise.Input{Procedure: inheritProc(t), Run: tree[0], Tree: tree,
		Exports: []supervise.Export{x}, Sources: []supervise.Source{one, two}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	sameFindings(t, res.Findings)
	return rules(res)
}

// TestASourceWhoseLogRefusedAConflictLeavesTheRulesThatReadReportsNotChecked:
// a log keeps the first content an observation id arrives with and only
// counts the others, so the copy a source's log refused may be the call a
// rule that reads its reports looks for. Any conflict its import reports
// count is doubt, whether or not the run's observations came from that
// import; a source whose log counted none changes nothing.
func TestASourceWhoseLogRefusedAConflictLeavesTheRulesThatReadReportsNotChecked(t *testing.T) {
	all := everyRuleIs(checked)
	for _, id := range []string{"RESOURCE_OUTSIDE_RUN", "DENIED_ACTION_RETRIED_ARGUMENTS",
		"DENIED_ACTION_RETRIED_RESOURCE", "DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN"} {
		all[id] = checked
	}
	if got := conflictingSources(t, 0, 0); !maps.Equal(got, all) {
		t.Fatalf("no conflict: rules %v, want %v", got, all)
	}
	const tail = " is in doubt: an import found an observation id with two contents"
	for name, c := range map[string]struct {
		s1, s2 uint64
		why    string
	}{
		"one in s2's log":  {0, 1, `source "s2"` + tail},
		"many in s1's log": {7, 0, `source "s1"` + tail},
		"in both logs":     {1, 1, `source "s1" and 1 more are in doubt: an import found an observation id with two contents`},
	} {
		t.Run(name, func(t *testing.T) {
			want := maps.Clone(all)
			want["STEP_OUTSIDE_PROCEDURE"] = "RULE_STATE_NOT_CHECKED:" + c.why
			want["DENIED_ACTION_RETRIED_AROUND"] = "RULE_STATE_NOT_CHECKED:" + c.why
			want["EXCEPTION_TAKEN"] = "RULE_STATE_NOT_CHECKED:exceptions on STEP_OUTSIDE_PROCEDURE are not judged: " + c.why
			if got := conflictingSources(t, c.s1, c.s2); !maps.Equal(got, want) {
				t.Fatalf("rules %v, want %v", got, want)
			}
		})
	}
}

// TestALongSourceIDWithAConflictIsQuotedAndCut: the why quotes at most 64
// bytes of the source id, as it does a request's.
func TestALongSourceIDWithAConflictIsQuotedAndCut(t *testing.T) {
	src := source()
	src.SourceID, src.Conflicts = "s"+strings.Repeat("x", 12000), 1
	res := evaluate(t, supervise.Input{Procedure: procWith(t), Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(conforming()...)}, Sources: []supervise.Source{src}})
	want := `RULE_STATE_NOT_CHECKED:source "s` + strings.Repeat("x", 63) +
		`" (truncated) is in doubt: an import found an observation id with two contents`
	if got := rules(res)["STEP_OUTSIDE_PROCEDURE"]; got != want {
		t.Fatalf("STEP_OUTSIDE_PROCEDURE %s, want %s", got, want)
	}
}

// TestAFindingOnASourceWithAConflictIsIndeterminate: drop_table, reported by
// s1 alone, is a call outside the procedure; once s1's log counted a
// conflict the finding stands, indeterminate, since the refused copy may be
// the one that was made.
func TestAFindingOnASourceWithAConflictIsIndeterminate(t *testing.T) {
	p := procWith(t)
	for name, c := range map[string]struct {
		conflicts uint64
		verdict   controlv1.FindingVerdict
	}{"no conflict": {0, suspected}, "a conflict": {1, indetermin}} {
		t.Run(name, func(t *testing.T) {
			src := source(ob{id: "obs-1", name: "drop_table", span: "1000000000000001", at: 40 * time.Second})
			src.Conflicts = c.conflicts
			res := evaluate(t, supervise.Input{Procedure: p, Exports: []supervise.Export{export(conforming()...)},
				Sources: []supervise.Source{src}})
			sameFindings(t, res.Findings, want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, c.verdict,
				handID(tenant, proj, runID, "refund", "1", "STEP_OUTSIDE_PROCEDURE", "1", "drop_table"), obsRef("s1", "obs-1")))
		})
	}
}
