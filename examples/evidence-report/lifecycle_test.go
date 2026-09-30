package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// grammar is the lifecycle as a table typed here: for each stage, named as
// the plane's chain validator names its state, which kinds may follow it,
// one character per kind in the contract's order from ACTION_PROPOSED (1) to
// POLICY_RELOADED (11), y for yes.
var grammar = map[string]string{
	"the start of a trail":               "y.........y",
	"ACTION_PROPOSED":                    ".y.......yy",
	"POLICY_DECIDED":                     "..y.y..y.yy",
	"APPROVAL_REQUESTED":                 "...y....yyy",
	"a decided approval":                 "....y..y.yy",
	"an approval window nobody answered": "..y....y.yy",
	"ACTION_STARTED":                     ".....yy..yy",
	"a closed action":                    ".........yy",
}

// stagePrefixes reaches each stage with a request typed here, in the order
// of the reader's stages.
var stagePrefixes = []struct {
	state string
	steps []step
}{
	{"the start of a trail", nil},
	{"ACTION_PROPOSED", allowed[:1]},
	{"POLICY_DECIDED", allowed[:2]},
	{"APPROVAL_REQUESTED", approved[:3]},
	{"a decided approval", approved[:4]},
	{"an approval window nobody answered", expired[:4]},
	{"ACTION_STARTED", allowed[:3]},
	{"a closed action", allowed},
}

// appended is, by kind, an event the report takes whenever the lifecycle
// lets that kind follow, so a refusal can only be the step's own.
var appended = map[string]string{
	"ACTION_PROPOSED":    proposed("read_order"),
	"POLICY_DECIDED":     decided("ALLOW", "RULE_ALLOW"),
	"APPROVAL_REQUESTED": approval("PENDING"),
	"APPROVAL_DECIDED":   approval("APPROVED"),
	"ACTION_STARTED":     execution("x1"),
	"ACTION_COMPLETED":   ended("x1", "SUCCESS"),
	"ACTION_FAILED":      ended("x1", "FAILURE"),
	"ACTION_BLOCKED":     decided("DENY", "RULE_DENY"),
	"APPROVAL_EXPIRED":   approval("EXPIRED"),
	"FINDING_RAISED":     "",
	"POLICY_RELOADED":    "",
}

// TestLifecycleGrammarIsTheValidators holds the table above to the chain the
// plane validates, edge for edge, and the reader's own steps to both.
func TestLifecycleGrammarIsTheValidators(t *testing.T) {
	stageOf := map[string]stage{}
	for i, p := range stagePrefixes {
		stageOf[p.state] = stage(i)
	}
	checked := 0
	for _, s := range evidence.ChainSteps() {
		row, ok := grammar[s.From]
		switch {
		case !ok:
			t.Fatalf("the validator names state %q, which this table does not", s.From)
		case !s.Declared:
			if _, placed := steps[s.Kind]; placed || !s.Unplaceable {
				t.Errorf("kind %d after %s: placed by the reader %t, by the validator %t; want neither", int32(s.Kind), s.From, placed, !s.Unplaceable)
			}
		case int(s.Kind) > len(row):
			t.Errorf("%s is not a column of this table", s.Kind)
		default:
			checked++
			agrees(t, s, row[s.Kind-1] == 'y', stageOf)
		}
	}
	kinds := controlv1.EventKind(0).Descriptor().Values().Len() - 1
	if want := len(grammar) * kinds; checked != want {
		t.Fatalf("%d declared steps checked, want %d: the listing examined less than every stage and kind", checked, want)
	}
}

// agrees holds one of the validator's steps to the table and to the reader's
// steps: whether it is taken, and the stage it moves to.
func agrees(t *testing.T, s evidence.ChainStep, want bool, stageOf map[string]stage) {
	t.Helper()
	if s.Allowed != want {
		t.Errorf("%s after %s: the validator says %t, this table %t", kindName(s.Kind), s.From, s.Allowed, want)
	}
	mine, placed := steps[s.Kind]
	if !placed {
		t.Errorf("%s: the reader cannot place a kind the contract declares", s.Kind)
		return
	}
	if taken := follows(stageOf[s.From], mine.from); taken != s.Allowed {
		t.Errorf("%s after %s: the reader says %t, the validator %t", kindName(s.Kind), s.From, taken, s.Allowed)
	}
	to := mine.to
	if to == unchanged {
		to = stageOf[s.From]
	}
	if s.Allowed && to != stageOf[s.To] {
		t.Errorf("%s after %s: the reader moves to stage %d, the validator to %s", kindName(s.Kind), s.From, to, s.To)
	}
}

// TestLifecycleGrammar runs every stage and every kind through the report:
// each step the table allows is taken, and each other one is refused as the
// step it is.
func TestLifecycleGrammar(t *testing.T) {
	kinds := controlv1.EventKind(0).Descriptor().Values()
	undeclared := int(kinds.Get(kinds.Len()-1).Number()) + 1
	for _, p := range stagePrefixes {
		prev := "the start of the request"
		if len(p.steps) > 0 {
			prev = p.steps[len(p.steps)-1].kind
		}
		for k := 1; k < kinds.Len(); k++ {
			kind := strings.TrimPrefix(string(kinds.ByNumber(controlv1.EventKind(k).Number()).Name()), "EVENT_KIND_")
			allowed := grammar[p.state][k-1] == 'y'
			t.Run(p.state+"/"+kind, func(t *testing.T) {
				note := oneRow(t, then(p.steps, step{kind, appended[kind]}))[10]
				switch {
				case !allowed && note != kind+" cannot follow "+prev:
					t.Errorf("%s after %s: note %q, want it refused", kind, p.state, note)
				case allowed && note != "-" && note != "no event ends the action" && note != "no event proposes an action":
					t.Errorf("%s after %s: note %q, want the step taken", kind, p.state, note)
				}
			})
		}
		t.Run(p.state+"/undeclared", func(t *testing.T) { cannotPlace(t, p.steps, undeclared) })
	}
}

// cannotPlace holds a kind no build declares, after prefix, to an unknown row.
func cannotPlace(t *testing.T, prefix []step, kind int) {
	events := chain("r1", then(prefix, step{"ACTION_PROPOSED", ""})...)
	last := len(events) - 1
	events[last] = strings.Replace(events[last], `"EVENT_KIND_ACTION_PROPOSED"`, strconv.Itoa(kind), 1)
	got := report(t, newExport("1.0").event(events...).whole())
	rows, _ := table(t, got.stdout)
	want := fmt.Sprintf("event r1-%d has kind %d, which this reader cannot place", last+1, kind)
	if len(rows) != 1 || !strings.HasSuffix(rows[0], "\tunknown\t"+want) {
		t.Errorf("rows %q, want one unknown: %s", rows, want)
	}
}

// oneRow runs one request through the report and returns its row's cells.
func oneRow(t *testing.T, s []step) []string {
	t.Helper()
	got := report(t, newExport("1.0").event(chain("r1", s...)...).whole())
	rows, _ := table(t, got.stdout)
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1:\n%s", len(rows), got.stdout)
	}
	return strings.Split(rows[0], "\t")
}

// then is a followed by b, a left as it was.
func then(a []step, b ...step) []step { return append(append([]step(nil), a...), b...) }

func unknownOne(name, in, row string) reportCase {
	return reportCase{name: name, in: in, rows: []string{row},
		totals: "totals: requests 1, completed 0, failed 0, aborted 0, blocked 0, open 0, unknown 1; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached,
		code:   1, stderr: "not every request"}
}

func whole(req string, s ...step) string { return newExport("1.0").event(chain(req, s...)...).whole() }

// TestLifecycleShapes holds what a chain of events cannot become: a cycle
// beside the chain, a start that names no execution, and an unanswered
// window asked again and answered.
func TestLifecycleShapes(t *testing.T) {
	r1 := chain("r1", allowed...)
	cycle := concat(r1, []string{
		evt("r1", "r1-5", "r1-6", "FINDING_RAISED", ""),
		evt("r1", "r1-6", "r1-5", "FINDING_RAISED", ""),
	})
	again := then(expired[:4], step{"APPROVAL_REQUESTED", approval("PENDING")}, step{"APPROVAL_DECIDED", approval("APPROVED")},
		step{"ACTION_STARTED", execution("x2")}, step{"ACTION_COMPLETED", ended("x2", "SUCCESS")})
	cases := []reportCase{
		unknownOne("two events that follow each other beside the chain", newExport("1.0").event(cycle...).whole(),
			"t\tp\tr1\trun-r1\tread_order\tALLOW\tRULE_ALLOW\tno\t-\tunknown\t2 of the request's events are not on the chain from r1-1"),
		unknownOne("a start that names no execution", whole("r1", allowed[0], allowed[1], step{"ACTION_STARTED", ""},
			step{"ACTION_COMPLETED", `,"result":{"status":"RESULT_STATUS_SUCCESS"}`}),
			"t\tp\tr1\trun-r1\tread_order\tALLOW\tRULE_ALLOW\tno\t-\tunknown\tevent r1-3 is ACTION_STARTED and names no execution"),
		{name: "an expired window asked again, answered and run", in: whole("r5", again...),
			rows:   []string{"t\tp\tr5\trun-r5\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\tyes\tapproved\tcompleted\t-"},
			totals: "totals: requests 1, completed 1, failed 0, aborted 0, blocked 0, open 0, unknown 0; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { check(t, tc) })
	}
}

// TestPayloads holds that an event takes its step only with what the step
// needs: the action proposed, a verdict the contract declares, an answer.
func TestPayloads(t *testing.T) {
	withDecision := func(d string) string {
		return whole("r1", allowed[0], step{"POLICY_DECIDED", `,"decision":` + d}, allowed[2], allowed[3])
	}
	answered := func(a string) string {
		return whole("r3", approved[0], approved[1], approved[2], step{"APPROVAL_DECIDED", a}, approved[4], approved[5])
	}
	cases := []reportCase{
		unknownOne("an empty decision", withDecision(`{}`),
			"t\tp\tr1\trun-r1\tread_order\tUNSPECIFIED\t-\tno\t-\tunknown\tevent r1-2 is POLICY_DECIDED and decides no verdict"),
		unknownOne("an unspecified verdict", withDecision(`{"verdict":"VERDICT_UNSPECIFIED","reasonCodes":["RULE_ALLOW"]}`),
			"t\tp\tr1\trun-r1\tread_order\tUNSPECIFIED\tRULE_ALLOW\tno\t-\tunknown\tevent r1-2 is POLICY_DECIDED and decides no verdict"),
		unknownOne("a verdict the contract does not declare", withDecision(`{"verdict":99,"reasonCodes":["RULE_ALLOW"]}`),
			"t\tp\tr1\trun-r1\tread_order\t99\tRULE_ALLOW\tno\t-\tunknown\tevent r1-2 decides verdict 99, which this reader does not declare"),
		unknownOne("a proposal with no payload", whole("r1", step{"ACTION_PROPOSED", ""}, allowed[1], allowed[2], allowed[3]),
			"t\tp\tr1\trun-r1\t-\tALLOW\tRULE_ALLOW\tno\t-\tunknown\tevent r1-1 is ACTION_PROPOSED and names no action"),
		unknownOne("a proposal with no action", whole("r1", step{"ACTION_PROPOSED", `,"proposed":{}`}, allowed[1], allowed[2], allowed[3]),
			"t\tp\tr1\trun-r1\t-\tALLOW\tRULE_ALLOW\tno\t-\tunknown\tevent r1-1 is ACTION_PROPOSED and names no action"),
		unknownOne("an answer with no state", answered(`,"approval":{}`),
			"t\tp\tr3\trun-r3\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\tyes\tunknown\tunknown\tevent r3-4 decides the approval as APPROVAL_STATE_UNSPECIFIED"),
		unknownOne("an answer with no approval", answered(""),
			"t\tp\tr3\trun-r3\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\tyes\tunknown\tunknown\tevent r3-4 decides the approval as APPROVAL_STATE_UNSPECIFIED"),
		unknownOne("an answer the contract does not declare", answered(`,"approval":{"state":99}`),
			"t\tp\tr3\trun-r3\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\tyes\tunknown\tunknown\tevent r3-4 decides the approval as 99"),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { check(t, tc) })
	}
}

// TestResults holds the end to what the result says: completed only on a
// success, aborted when nothing was sent, failed on a failure or a timeout,
// and unknown whenever the effect may have happened.
func TestResults(t *testing.T) {
	closed := func(kind, extra string) string {
		return whole("r1", allowed[0], allowed[1], allowed[2], step{kind, extra})
	}
	one := func(end string) string {
		counts := map[string]int{}
		counts[end] = 1
		return fmt.Sprintf("totals: requests 1, completed %d, failed %d, aborted %d, blocked 0, open 0, unknown %d; "+
			"gaps 0, duplicates 0, conflicting 0, refused 0"+endReached, counts["completed"], counts["failed"], counts["aborted"], counts["unknown"])
	}
	row := "t\tp\tr1\trun-r1\tread_order\tALLOW\tRULE_ALLOW\tno\t-\t"
	mayHave := func(kind, status string) reportCase {
		return unknownOne(kind+" "+status, closed(kind, ended("x1", status)),
			row+"unknown\tevent r1-4 is "+kind+" with result RESULT_STATUS_"+status+": the effect may have happened")
	}
	cases := []reportCase{
		{name: "a success", in: closed("ACTION_COMPLETED", ended("x1", "SUCCESS")), rows: []string{row + "completed\t-"}, totals: one("completed")},
		{name: "nothing sent", in: closed("ACTION_FAILED", ended("x1", "BLOCKED")), rows: []string{row + "aborted\t-"}, totals: one("aborted")},
		{name: "a failure", in: closed("ACTION_FAILED", ended("x1", "FAILURE")), rows: []string{row + "failed\t-"}, totals: one("failed")},
		{name: "a timeout", in: closed("ACTION_FAILED", ended("x1", "TIMEOUT")), rows: []string{row + "failed\t-"}, totals: one("failed")},
		mayHave("ACTION_COMPLETED", "FAILURE"),
		mayHave("ACTION_COMPLETED", "BLOCKED"),
		mayHave("ACTION_COMPLETED", "UNKNOWN"),
		mayHave("ACTION_FAILED", "SUCCESS"),
		mayHave("ACTION_FAILED", "UNKNOWN"),
		mayHave("ACTION_FAILED", "UNSPECIFIED"),
		mayHave("ACTION_FAILED", "CANCELLED"),
		unknownOne("a result the contract does not declare", closed("ACTION_FAILED", execution("x1")+`,"result":{"status":99}`),
			row+"unknown\tevent r1-4 is ACTION_FAILED with result 99: the effect may have happened"),
		unknownOne("a completion with no result", closed("ACTION_COMPLETED", execution("x1")),
			row+"unknown\tevent r1-4 is ACTION_COMPLETED and carries no result: the effect may have happened"),
		unknownOne("a failure with no result", closed("ACTION_FAILED", execution("x1")),
			row+"unknown\tevent r1-4 is ACTION_FAILED and carries no result: the effect may have happened"),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { check(t, tc) })
	}
}

func inMode(events []string, mode string) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = strings.ReplaceAll(e, `"ENFORCEMENT_MODE_ENFORCE"`, mode)
	}
	return out
}

// TestEnforcement holds a run to its decision in every mode that enforces
// one, and shows a run in a mode that enforces nothing as it ran.
func TestEnforcement(t *testing.T) {
	ran := func(verdict string) []step {
		return []step{{"ACTION_PROPOSED", proposed("refund")}, {"POLICY_DECIDED", decided(verdict, "RULE_X")},
			{"ACTION_STARTED", execution("x1")}, {"ACTION_COMPLETED", ended("x1", "SUCCESS")}}
	}
	rejectedRan := then(rejected[:4], step{"ACTION_STARTED", execution("x1")}, step{"ACTION_COMPLETED", ended("x1", "SUCCESS")})
	allowRejectedRan := then([]step{allowed[0], allowed[1], {"APPROVAL_REQUESTED", approval("PENDING")},
		{"APPROVAL_DECIDED", approval("REJECTED")}}, allowed[2], allowed[3])
	inModeWhole := func(mode string, s []step) string {
		return newExport("1.0").event(inMode(chain("r2", s...), `"`+mode+`"`)...).whole()
	}
	against := func(name, mode, verdict string) reportCase {
		short := strings.TrimPrefix(mode, "ENFORCEMENT_MODE_")
		return unknownOne(name, inModeWhole(mode, ran(verdict)),
			"t\tp\tr2\trun-r2\trefund\t"+verdict+"\tRULE_X\tno\t-\tunknown\tevent r2-3 starts the action under "+short+
				" after verdict "+verdict+" and no approval: it ran against its decision")
	}
	observed := func(mode, verdict string) reportCase {
		short := strings.TrimPrefix(mode, "ENFORCEMENT_MODE_")
		return reportCase{name: verdict + " run under " + short, in: inModeWhole(mode, ran(verdict)),
			rows: []string{"t\tp\tr2\trun-r2\trefund\t" + verdict + "\tRULE_X\tno\t-\tcompleted\tran under " + short +
				", which enforces nothing, after verdict " + verdict + " and no approval"},
			totals: "totals: requests 1, completed 1, failed 0, aborted 0, blocked 0, open 0, unknown 0; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached}
	}
	drop := func(events []string, i int) []string {
		out := append([]string(nil), events...)
		out[i] = strings.Replace(out[i], `,"enforcementMode":"ENFORCEMENT_MODE_ENFORCE"`, "", 1)
		return out
	}
	swap := func(events []string, i int, mode string) []string {
		out := append([]string(nil), events...)
		out[i] = strings.Replace(out[i], `"ENFORCEMENT_MODE_ENFORCE"`, mode, 1)
		return out
	}
	r1 := chain("r1", allowed...)
	allowRow := "t\tp\tr1\trun-r1\tread_order\tALLOW\tRULE_ALLOW\tno\t-\tunknown\t"
	cases := []reportCase{
		against("a denied action run under ENFORCE", "ENFORCEMENT_MODE_ENFORCE", "DENY"),
		against("an undetermined action run under ENFORCE", "ENFORCEMENT_MODE_ENFORCE", "INDETERMINATE"),
		against("an action run without the approval it needed", "ENFORCEMENT_MODE_ENFORCE", "REQUIRE_APPROVAL"),
		against("a denied action run under APPROVE", "ENFORCEMENT_MODE_APPROVE", "DENY"),
		against("a denied action run under LOCKDOWN", "ENFORCEMENT_MODE_LOCKDOWN", "DENY"),
		against("a denied action run under SHADOW", "ENFORCEMENT_MODE_SHADOW", "DENY"),
		unknownOne("an action run after its approval was rejected", inModeWhole("ENFORCEMENT_MODE_ENFORCE", rejectedRan),
			"t\tp\tr2\trun-r2\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\tyes\trejected\tunknown\t"+
				"event r2-5 starts the action under ENFORCE after its approval was rejected: it ran against its decision"),
		unknownOne("an allowed action run after its approval was rejected", inModeWhole("ENFORCEMENT_MODE_APPROVE", allowRejectedRan),
			"t\tp\tr2\trun-r2\tread_order\tALLOW\tRULE_ALLOW\tyes\trejected\tunknown\t"+
				"event r2-5 starts the action under APPROVE after its approval was rejected: it ran against its decision"),
		observed("ENFORCEMENT_MODE_OBSERVE", "DENY"),
		observed("ENFORCEMENT_MODE_WARN", "INDETERMINATE"),
		unknownOne("an event that names no mode", newExport("1.0").event(drop(r1, 2)...).whole(),
			allowRow+"event r1-3 names no enforcement mode"),
		unknownOne("a mode the contract does not declare", newExport("1.0").event(swap(r1, 2, "99")...).whole(),
			allowRow+"event r1-3 names enforcement mode 99, which this reader does not declare"),
		unknownOne("a mode that changes within the request", newExport("1.0").event(swap(r1, 2, `"ENFORCEMENT_MODE_OBSERVE"`)...).whole(),
			allowRow+"event r1-3 names enforcement mode OBSERVE, and the request's first event ENFORCE"),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { check(t, tc) })
	}
}
