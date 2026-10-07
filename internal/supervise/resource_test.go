package supervise_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

const ruleResource = "RESOURCE_OUTSIDE_RUN"

// TestTwoResourcesOfOneBindingFireWhateverComesFirst: 42 then 43 and 43 then
// 42 fire alike, with one id; no first call fixes what the run holds, and a
// denied probe counts.
func TestTwoResourcesOfOneBindingFireWhateverComesFirst(t *testing.T) {
	for name, x := range map[string]supervise.Export{
		"42 then 43": acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("43"))),
		"43 then 42": acts(lookupOf("r1", 0, orderNo("43")), refundOf("r2", 10*time.Second, orderNo("42"))),
		"the first call denied": acts(lookupOf("r1", 0, orderNo("42")).ending("deny"),
			refundOf("r2", 10*time.Second, orderNo("43"))),
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, family(), x)
			names(t, res, ruleResource, runID, id02(ruleResource, "order", runID))
			f := only(res, ruleResource)[0]
			if f.GetFinding().GetVerdict() != confirmed || citedEvents(f) != "r1-e1 r2-e1" {
				t.Fatalf("%s citing %q", f.GetFinding().GetVerdict(), citedEvents(f))
			}
		})
	}
}

// TestOneResourceOfABindingIsNoFinding: 42 called twice, through both tools,
// is what the run holds; a resource of another binding is that binding's.
func TestOneResourceOfABindingIsNoFinding(t *testing.T) {
	customer := &controlv1.Resource{Type: "crm_customer", Id: "43", TenantId: tenant, Environment: "prod"}
	res := supervise02(t, family(), acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("42")),
		act{call: call{req: "r3", tool: "get_customer", upstream: "crm", at: 20 * time.Second}, res: customer}))
	if got := only(res, ruleResource); len(got) != 0 {
		t.Fatalf("findings:\n%s", dump(got))
	}
	if got := rules(res)[ruleResource]; got != checked {
		t.Fatalf("%s is %s", ruleResource, got)
	}
}

// TestCallsWithNoIdLeaveTwoIdsConfirmedAndOneInDoubt: two ids beside a call
// with none stay confirmed; one id beside a call with none, or beside one of
// another type, is indeterminate.
func TestCallsWithNoIdLeaveTwoIdsConfirmedAndOneInDoubt(t *testing.T) {
	none := refundOf("r3", 20*time.Second, nil)
	noID := refundOf("r3", 20*time.Second, &controlv1.Resource{Type: "shop_order", TenantId: tenant, Environment: "prod"})
	otherType := refundOf("r3", 20*time.Second, &controlv1.Resource{Type: "invoice", Id: "43", TenantId: tenant, Environment: "prod"})
	for name, c := range map[string]struct {
		x    supervise.Export
		want string
	}{
		"two ids and no resource": {acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("43")), none),
			runID + " FINDING_VERDICT_CONFIRMED"},
		"one id and no resource":      {acts(lookupOf("r1", 0, orderNo("42")), none), runID + " FINDING_VERDICT_INDETERMINATE"},
		"one id and an empty id":      {acts(lookupOf("r1", 0, orderNo("42")), noID), runID + " FINDING_VERDICT_INDETERMINATE"},
		"one id and another type":     {acts(lookupOf("r1", 0, orderNo("42")), otherType), runID + " FINDING_VERDICT_INDETERMINATE"},
		"one id twice and no another": {acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("42"))), ""},
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, family(), c.x)
			if got := strings.Join(verdicts(res, ruleResource), ","); got != c.want {
				t.Fatalf("findings %q, want %q:\n%s", got, c.want, dump(res.Findings))
			}
		})
	}
}

// TestNoIdOfABindingLeavesTheRuleNotChecked: bound calls that carry no id,
// or only one of another type, judge nothing, and the report says why;
// a procedure whose entries bind nothing turns the rule off.
func TestNoIdOfABindingLeavesTheRuleNotChecked(t *testing.T) {
	const why = "RULE_STATE_NOT_CHECKED:no call of a binding carries a resource id of its type"
	for name, x := range map[string]supervise.Export{
		"no resource":      acts(lookupOf("r1", 0, nil), refundOf("r2", 10*time.Second, nil)),
		"another type":     acts(lookupOf("r1", 0, &controlv1.Resource{Type: "invoice", Id: "7"})),
		"no bound call":    acts(act{call: call{req: "r1", tool: "search_docs", upstream: "docs"}, res: orderNo("42")}),
		"the binding's id": acts(lookupOf("r1", 0, orderNo("42"))),
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, family(), x)
			want := why
			if name == "the binding's id" {
				want = checked
			}
			if got := rules(res)[ruleResource]; got != want {
				t.Fatalf("%s is %q, want %q", ruleResource, got, want)
			}
		})
	}
	doc := strings.NewReplacer(`"binds": ["order"]`, `"binds": []`, `"binds": ["customer"]`, `"binds": []`).Replace(v02Doc(t))
	p, err := supervise.ReadProcedure([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	res := treeEvaluate(t, supervise.Input{Procedure: p, Tree: family(),
		Exports: []supervise.Export{acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("43")))}})
	if got := rules(res)[ruleResource]; got != "RULE_STATE_OFF:no step or allowed tool binds a resource" || len(only(res, ruleResource)) != 0 {
		t.Fatalf("%s is %q with findings:\n%s", ruleResource, got, dump(res.Findings))
	}
}

// TestEachByteOfAResourceTellsItApart: an id spelled two ways through two
// tools of one binding is two resources, and so is one id of another
// resource tenant or environment; nothing is normalised.
func TestEachByteOfAResourceTellsItApart(t *testing.T) {
	for name, second := range map[string]*controlv1.Resource{
		`"042" beside "42"`:       orderNo("042"),
		"another resource tenant": {Type: "shop_order", Id: "42", TenantId: "t2", Environment: "prod"},
		"another environment":     {Type: "shop_order", Id: "42", TenantId: tenant, Environment: "staging"},
		"a trailing space":        orderNo("42 "),
		"the tenant in capitals":  {Type: "shop_order", Id: "42", TenantId: strings.ToUpper(tenant), Environment: "prod"},
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, family(), acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, second)))
			if got := verdicts(res, ruleResource); !slices.Equal(got, []string{runID + " FINDING_VERDICT_CONFIRMED"}) {
				t.Fatalf("findings %q:\n%s", got, dump(res.Findings))
			}
		})
	}
}

// TestEveryRunThatCalledABindingIsNamed: under inherit 42 in one child and
// 43 in its sibling name both, each by its own anchor and citing its own
// calls first, then the first call of each other resource; a run that called
// the binding with no id is named too.
func TestEveryRunThatCalledABindingIsNamed(t *testing.T) {
	res := supervise02(t, siblings(), acts(lookupOf("a1", 0, orderNo("42")).in(childRun),
		refundOf("b1", 10*time.Second, orderNo("43")).in(siblingRun), lookupOf("r1", 20*time.Second, nil),
		refundOf("a2", 30*time.Second, orderNo("42")).in(childRun)))
	got := only(res, ruleResource)
	want := []struct{ run, cites string }{{runID, "r1-e1 a1-e1 b1-e1"}, {childRun, "a1-e1 a2-e1 b1-e1"}, {siblingRun, "b1-e1 a1-e1"}}
	if len(got) != len(want) {
		t.Fatalf("%d findings, want %d:\n%s", len(got), len(want), dump(res.Findings))
	}
	for i, w := range want {
		f := got[i].GetFinding()
		if f.GetRunId() != w.run || f.GetFindingId() != id02(ruleResource, "order", w.run) || f.GetVerdict() != confirmed ||
			citedEvents(got[i]) != w.cites {
			t.Errorf("finding %d names %s with id %s, %s, citing %q; want %s citing %q", i, f.GetRunId(), f.GetFindingId(),
				f.GetVerdict(), citedEvents(got[i]), w.run, w.cites)
		}
	}
}

// TestATrailInDoubtConfirmsNoResource: a second id only a trail in doubt
// carries leaves the finding indeterminate; two ids from coherent trails
// stay confirmed beside it, and a run whose only call is in doubt is not
// confirmed to have made it.
func TestATrailInDoubtConfirmsNoResource(t *testing.T) {
	doubted := acts(refundOf("b1", 10*time.Second, orderNo("43")).in(siblingRun))
	conflict := acts(refundOf("b1", 10*time.Second, orderNo("43")).in(siblingRun))
	conflict.Events[2].OccurredAt = at(time.Hour)
	for name, c := range map[string]struct {
		exports []supervise.Export
		want    []string
	}{
		"the second id in doubt": {[]supervise.Export{acts(lookupOf("a1", 0, orderNo("42")).in(childRun)), doubted, conflict},
			[]string{childRun + " FINDING_VERDICT_INDETERMINATE", siblingRun + " FINDING_VERDICT_INDETERMINATE"}},
		"two coherent ids beside it": {[]supervise.Export{acts(lookupOf("a1", 0, orderNo("42")).in(childRun),
			lookupOf("a2", 5*time.Second, orderNo("44")).in(childRun)), doubted, conflict},
			[]string{childRun + " FINDING_VERDICT_CONFIRMED", siblingRun + " FINDING_VERDICT_INDETERMINATE"}},
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, siblings(), c.exports...)
			if got := verdicts(res, ruleResource); !slices.Equal(got, c.want) {
				t.Fatalf("findings %q, want %q:\n%s", got, c.want, dump(res.Findings))
			}
		})
	}
}

// TestManyResourcesWriteBoundedRecords: a run that probes many ids gets one
// record per run that cites at most MaxFindingRefs and counts the rest.
func TestManyResourcesWriteBoundedRecords(t *testing.T) {
	var as []act
	for i := range 300 {
		run := []string{childRun, siblingRun}[i%2]
		as = append(as, lookupOf(fmt.Sprintf("q%03d", i), time.Duration(i)*time.Second, orderNo(fmt.Sprint(i))).in(run))
	}
	res := supervise02(t, siblings(), acts(as...))
	got := only(res, ruleResource)
	if len(got) != 2 {
		t.Fatalf("%d findings:\n%s", len(got), dump(res.Findings))
	}
	for _, f := range got {
		// Each run's 150 calls, and the 150 of the other run that each name an id.
		if len(f.GetRefs()) != supervise.MaxFindingRefs || f.GetRefsLeftOut() != 300-supervise.MaxFindingRefs {
			t.Errorf("%s cites %d and leaves out %d", f.GetFinding().GetRunId(), len(f.GetRefs()), f.GetRefsLeftOut())
		}
		lineOf(t, f)
	}
}

// TestAFindingInDoubtCitesWhatLeavesItSo: one id in one child and a call
// with none in its sibling leave both runs indeterminate, each citing its own
// call and then the other's.
func TestAFindingInDoubtCitesWhatLeavesItSo(t *testing.T) {
	res := supervise02(t, siblings(), acts(lookupOf("a1", 0, orderNo("42")).in(childRun),
		refundOf("b1", 10*time.Second, nil).in(siblingRun)))
	got := only(res, ruleResource)
	if len(got) != 2 || citedEvents(got[0]) != "a1-e1 b1-e1" || citedEvents(got[1]) != "b1-e1 a1-e1" ||
		got[0].GetFinding().GetVerdict() != indetermin || got[1].GetFinding().GetVerdict() != indetermin {
		t.Fatalf("findings:\n%s", dump(res.Findings))
	}
}

// TestABindingCalledWithNoIdIsNeverReadAsChecked: beside a binding whose
// calls carry ids, one whose calls carry none of its type is a finding of
// that binding, indeterminate, per run that called it. The agent chooses
// whether a call carries an id, so leaving one out must neither pass for
// that binding nor turn the rule off for another binding that fires.
func TestABindingCalledWithNoIdIsNeverReadAsChecked(t *testing.T) {
	mail := act{call: call{req: "b1", tool: "send_mail", upstream: "mail", at: 20 * time.Second, run: siblingRun}}
	otherType := mail
	otherType.res = orderNo("7")
	for name, c := range map[string]struct {
		x    supervise.Export
		want []string
	}{
		"beside a binding that fires": {acts(lookupOf("a1", 0, orderNo("42")).in(childRun),
			refundOf("a2", 10*time.Second, orderNo("43")).in(childRun), mail),
			[]string{childRun + " FINDING_VERDICT_CONFIRMED", siblingRun + " FINDING_VERDICT_INDETERMINATE"}},
		"beside one that holds one id": {acts(lookupOf("a1", 0, orderNo("42")).in(childRun), mail),
			[]string{siblingRun + " FINDING_VERDICT_INDETERMINATE"}},
		"an id of another type": {acts(lookupOf("a1", 0, orderNo("42")).in(childRun), otherType),
			[]string{siblingRun + " FINDING_VERDICT_INDETERMINATE"}},
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, siblings(), c.x)
			if got := rules(res)[ruleResource]; got != checked {
				t.Fatalf("%s is %q", ruleResource, got)
			}
			if got := verdicts(res, ruleResource); !slices.Equal(got, c.want) {
				t.Fatalf("findings %q, want %q:\n%s", got, c.want, dump(res.Findings))
			}
			customer := only(res, ruleResource)[len(c.want)-1]
			if f := customer.GetFinding(); f.GetFindingId() != id02(ruleResource, "customer", siblingRun) || citedEvents(customer) != "b1-e1" {
				t.Fatalf("the customer binding's finding is %s citing %q", f.GetFindingId(), citedEvents(customer))
			}
		})
	}
}
