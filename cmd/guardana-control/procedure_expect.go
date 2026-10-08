package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

// caseExpect is what a case expects: the state of every rule the report
// lists, every finding, and every source never heard, silent or not read,
// each list sorted.
type caseExpect struct {
	rules                       map[string]findingv1alpha1.RuleState
	findings                    []caseFinding
	neverHeard, silent, notRead []string
}

// caseFinding is a finding as a case states it: its rule, its verdict, the
// run it names, and the requests and observations it cites, each sorted.
type caseFinding struct {
	rule, run              string
	verdict                controlv1.FindingVerdict
	requests, observations []string
}

func (f caseFinding) String() string {
	s := fmt.Sprintf("%s %s %s [%s]", f.rule, findingVerdictName(f.verdict), f.run, strings.Join(f.requests, " "))
	if len(f.observations) > 0 {
		s += " observations [" + strings.Join(f.observations, " ") + "]"
	}
	return s
}

// differences names each field in which f, a finding made, differs from want.
func (f caseFinding) differences(want caseFinding) []string {
	var out []string
	if f.rule != want.rule {
		out = append(out, "rule "+f.rule+", want "+want.rule)
	}
	if f.verdict != want.verdict {
		out = append(out, "verdict "+findingVerdictName(f.verdict)+", want "+findingVerdictName(want.verdict))
	}
	if f.run != want.run {
		out = append(out, "run "+f.run+", want "+want.run)
	}
	if !slices.Equal(f.requests, want.requests) {
		out = append(out, "requests ["+strings.Join(f.requests, " ")+"], want ["+strings.Join(want.requests, " ")+"]")
	}
	if !slices.Equal(f.observations, want.observations) {
		out = append(out, "observations ["+strings.Join(f.observations, " ")+"], want ["+strings.Join(want.observations, " ")+"]")
	}
	return out
}

func findingVerdictName(v controlv1.FindingVerdict) string {
	return strings.TrimPrefix(v.String(), "FINDING_VERDICT_")
}

func ruleStateName(s findingv1alpha1.RuleState) string {
	return strings.TrimPrefix(s.String(), "RULE_STATE_")
}

// foundOf is each finding record as a case states a finding.
func foundOf(records []*findingv1alpha1.FindingRecord) []caseFinding {
	out := make([]caseFinding, 0, len(records))
	for _, r := range records {
		f := caseFinding{rule: r.GetFinding().GetRuleId(), run: r.GetFinding().GetRunId(), verdict: r.GetFinding().GetVerdict()}
		for _, ref := range r.GetRefs() {
			if e := ref.GetEvent(); e != nil {
				f.requests = append(f.requests, e.GetRequestId())
			}
			if o := ref.GetObservation(); o != nil {
				f.observations = append(f.observations, o.GetObservationId())
			}
		}
		slices.Sort(f.requests)
		slices.Sort(f.observations)
		f.requests, f.observations = slices.Compact(f.requests), slices.Compact(f.observations)
		out = append(out, f)
	}
	return out
}

// compareRules names each rule whose state differs from the one expected,
// each the report lists and the case does not, and each the case expects
// and the report does not list.
func compareRules(want map[string]findingv1alpha1.RuleState, got []*findingv1alpha1.RuleResult) []string {
	var out []string
	listed := map[string]bool{}
	for _, r := range got {
		listed[r.GetRuleId()] = true
		w, expected := want[r.GetRuleId()]
		switch {
		case !expected:
			out = append(out, fmt.Sprintf("rule %s: state %s, not expected", r.GetRuleId(), ruleStateName(r.GetState())))
		case w != r.GetState():
			out = append(out, fmt.Sprintf("rule %s: state %s, want %s", r.GetRuleId(), ruleStateName(r.GetState()), ruleStateName(w)))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(want)) {
		if !listed[id] {
			out = append(out, "rule "+id+": expected, not in the report")
		}
	}
	return out
}

// compareFindings matches the findings made against those expected, each
// at most once. What is left of the expected is paired with what is left of
// the made where the two differ in one field only, which is named; the
// rest is missing or unexpected.
func compareFindings(want, got []caseFinding) []string {
	want, got = slices.Clone(want), slices.Clone(got)
	matched := func(match func(w, g caseFinding) bool, report func(w, g caseFinding)) {
		for i := 0; i < len(want); {
			j := slices.IndexFunc(got, func(g caseFinding) bool { return match(want[i], g) })
			if j < 0 {
				i++
				continue
			}
			report(want[i], got[j])
			want, got = slices.Delete(want, i, i+1), slices.Delete(got, j, j+1)
		}
	}
	var out []string
	matched(func(w, g caseFinding) bool { return len(g.differences(w)) == 0 }, func(caseFinding, caseFinding) {})
	matched(func(w, g caseFinding) bool { return len(g.differences(w)) == 1 }, func(w, g caseFinding) {
		out = append(out, "finding "+w.String()+": "+g.differences(w)[0])
	})
	for _, w := range want {
		out = append(out, "missing finding "+w.String())
	}
	for _, g := range got {
		out = append(out, "unexpected finding "+g.String())
	}
	return out
}

// compareSources names each list of sources the supervision did not judge
// against that differs from the one expected: a source unheard fails a case
// that does not say so, since every rule that rests on it was weakened.
func compareSources(want caseExpect, res *supervise.Result) []string {
	var out []string
	for _, c := range []struct {
		what      string
		got, want []string
	}{
		{"never heard", res.NeverHeard, want.neverHeard},
		{"silent", res.Silent, want.silent},
		{"not read", res.SourcesNotRead, want.notRead},
	} {
		got := slices.Sorted(slices.Values(c.got))
		if !slices.Equal(got, c.want) {
			out = append(out, fmt.Sprintf("%s [%s], want [%s]", c.what, strings.Join(got, " "), strings.Join(c.want, " ")))
		}
	}
	return out
}
