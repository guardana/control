package policycompare

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestCompareCountsOverTheirOwnDenominators(t *testing.T) {
	r := compareOrFail(t, []Case{
		explicit("case:read.json", envelope("r1", "orders.read", read, "prod")),
		explicit("case:write.json", envelope("r2", "orders.update", write, "prod")),
		explicit("case:wire.json", envelope("r3", "wire", pay, "")),
		explicit("case:pay.json", envelope("r4", "pay", pay, "prod")),
		{ID: "request:r5", Source: SourceEvidence, Unknown: ReasonHistoricPolicyMissing},
	}, nil)
	checkFiveCaseCounts(t, r.Counts)
	if r.Complete() {
		t.Error("a report with unknown cases is complete")
	}
	if !r.Simulated || r.Kind != "policy-comparison/v1alpha1" {
		t.Errorf("kind %q simulated %v", r.Kind, r.Simulated)
	}
	for _, row := range r.Cases {
		if !row.Simulated {
			t.Errorf("%s is not marked simulated", row.ID)
		}
	}
	if got := rowOf(t, r, "case:write.json"); got.Change != Looser || *got.Before.Verdict != "DENY" || *got.After.Verdict != "ALLOW" {
		t.Errorf("write: %+v", got)
	}
}

func checkFiveCaseCounts(t *testing.T, c Counts) {
	t.Helper()
	if c.Read != 5 || c.BySource[SourceCase] != 4 || c.BySource[SourceEvidence] != 1 {
		t.Errorf("read %d by source %v, want 5 of which 4 cases and 1 evidence", c.Read, c.BySource)
	}
	if c.EvaluatedBothSides != (Share{Count: 4, Of: 5}) {
		t.Errorf("evaluated on both sides %+v, want 4 of 5", c.EvaluatedBothSides)
	}
	wantReasons := map[Reason]int{}
	for _, reason := range Reasons() {
		wantReasons[reason] = 0
	}
	wantReasons[ReasonMissingFacts], wantReasons[ReasonExternalAnswerNeeded], wantReasons[ReasonHistoricPolicyMissing] = 1, 1, 1
	if c.Unknown.Count != 3 || c.Unknown.Of != 5 || !maps.Equal(c.Unknown.ByReason, wantReasons) {
		t.Errorf("unknown %+v, want 3 of 5 with every reason keyed: one missing facts, one external answer, one historic policy", c.Unknown)
	}
	if c.Compared != (Share{Count: 2, Of: 5}) {
		t.Errorf("compared %+v, want 2 of 5", c.Compared)
	}
	if c.Change != (ChangeCount{Of: 2, Unchanged: 1, Looser: 1}) {
		t.Errorf("change %+v, want of 2: one unchanged, one looser", c.Change)
	}
}

// A case both sides decide alike on a missing fact is unknown, never
// unchanged, and neither side reports a verdict for it.
func TestUnknownNeverCollapsesIntoUnchanged(t *testing.T) {
	r := compareOrFail(t, []Case{
		explicit("case:wire.json", envelope("r1", "wire", pay, "")),
		explicit("case:pay.json", envelope("r2", "pay", pay, "prod")),
	}, nil)
	for id, want := range map[string]Reason{"case:wire.json": ReasonMissingFacts, "case:pay.json": ReasonExternalAnswerNeeded} {
		checkUnknownRow(t, rowOf(t, r, id), want)
	}
	if got := rowOf(t, r, "case:wire.json").Before.MissingFacts; !slices.Equal(got, []string{"resource.environment"}) {
		t.Errorf("wire's missing facts %q", got)
	}
	if r.Counts.Change.Unchanged != 0 || r.Counts.Compared.Count != 0 {
		t.Errorf("counts %+v", r.Counts)
	}
}

func checkUnknownRow(t *testing.T, row CaseRow, want Reason) {
	t.Helper()
	if row.Change != Unknown || reasonOf(row) != want {
		t.Errorf("%s: %s %q, want unknown %q", row.ID, row.Change, reasonOf(row), want)
	}
	if row.Before.Verdict != nil || row.After.Verdict != nil || row.Before.Action != nil || row.After.Codes != nil {
		t.Errorf("%s: an unknown case reports a decision: %+v %+v", row.ID, row.Before, row.After)
	}
	if !row.Before.Evaluated || !row.After.Evaluated || len(row.Before.MissingFacts) == 0 {
		t.Errorf("%s: %+v, want both sides evaluated with their missing facts", row.ID, row)
	}
}

func TestCompareIsCompleteOnlyWithNothingUnknown(t *testing.T) {
	one := compareOrFail(t, []Case{explicit("case:read.json", envelope("r1", "orders.read", read, "prod"))}, nil)
	if !one.Complete() {
		t.Errorf("one case evaluated alike on both sides: not complete: %+v", one.Counts)
	}
	notLoaded := explicit("case:bad.json", envelope("r2", "orders.read", read, "prod"))
	notLoaded.Input.Options.MaxStale = 0
	two := compareOrFail(t, []Case{explicit("case:read.json", envelope("r1", "orders.read", read, "prod")), notLoaded}, nil)
	if two.Complete() || reasonOf(rowOf(t, two, "case:bad.json")) != ReasonNotEvaluated || two.Counts.EvaluatedBothSides.Count != 1 {
		t.Errorf("a case the kernel refused: complete %v, %+v", two.Complete(), two.Counts)
	}
}

func TestCompareLabelCounts(t *testing.T) {
	cases := []Case{
		explicit("case:read.json", envelope("r1", "orders.read", read, "prod")),
		explicit("case:write.json", envelope("r2", "orders.update", write, "prod")),
		explicit("case:wire.json", envelope("r3", "wire", pay, "")),
		explicit("case:pay.json", envelope("r4", "pay", pay, "prod")),
	}
	labels := &Labels{Labeller: "review", ByCase: map[string]Label{
		"case:read.json": Legitimate, "case:write.json": Legitimate, "case:wire.json": Attack,
	}}
	r := compareOrFail(t, cases, labels)
	want := map[string]Tally{
		"attack":     {Of: 1, Unknown: 1},
		"legitimate": {Of: 2, Unchanged: 1, Looser: 1},
		"unlabelled": {Of: 1, Unknown: 1},
	}
	if !maps.Equal(r.Counts.ByLabel, want) {
		t.Errorf("by label %+v, want %+v", r.Counts.ByLabel, want)
	}
	if l := rowOf(t, r, "case:wire.json").Label; l == nil || *l != Attack {
		t.Errorf("wire's label %v", l)
	}
	if r.Scope.LabellerIndependence != "declared, not verified" {
		t.Errorf("labeller independence %q", r.Scope.LabellerIndependence)
	}
	_, err := Compare(Run{Current: signed(t, currentDoc), Candidate: signed(t, candidateDoc), Cases: cases,
		Labels: &Labels{Labeller: "review", ByCase: map[string]Label{"case:gone.json": Attack}}})
	if !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), `"case:gone.json"`) {
		t.Errorf("a label naming no case of the run: %v", err)
	}
}

func TestCompareRefusesARunItCannotCount(t *testing.T) {
	unknown := func(n int) []Case {
		out := make([]Case, n)
		for i := range out {
			out[i] = Case{ID: fmt.Sprintf("request:r%d", i), Source: SourceEvidence, Unknown: ReasonHistoricPolicyMissing}
		}
		return out
	}
	run := func(cases []Case) error {
		_, err := Compare(Run{Current: signed(t, currentDoc), Candidate: signed(t, candidateDoc), Cases: cases})
		return err
	}
	if err := run(unknown(4096)); err != nil {
		t.Errorf("4096 cases: %v", err)
	}
	if err := run(unknown(4097)); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "4097 cases, at most 4096") {
		t.Errorf("4097 cases: %v, want the bound", err)
	}
	if err := run(nil); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "no case") {
		t.Errorf("no case: %v", err)
	}
	twice := append(unknown(1), unknown(1)...)
	if err := run(twice); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "twice") {
		t.Errorf("one id twice: %v", err)
	}
}
