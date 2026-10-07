package supervise_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	high       = controlv1.FindingSeverity_FINDING_SEVERITY_HIGH
	medium     = controlv1.FindingSeverity_FINDING_SEVERITY_MEDIUM
	alert      = findingv1alpha1.Escalation_ESCALATION_ALERT
	confirmed  = controlv1.FindingVerdict_FINDING_VERDICT_CONFIRMED
	suspected  = controlv1.FindingVerdict_FINDING_VERDICT_SUSPECTED
	indetermin = controlv1.FindingVerdict_FINDING_VERDICT_INDETERMINATE
	checked    = "RULE_STATE_CHECKED:"
)

// conforming is the refund done in order, with a search between, every call
// completed: lookup r1, search r2, refund r3, notify r4.
func conforming() []call {
	return []call{
		{req: "r1", tool: "get_order", upstream: "shop", span: "1111111111111111"},
		{req: "r2", tool: "search_docs", upstream: "docs", at: 10 * time.Second},
		{req: "r3", tool: "issue_refund", upstream: "pay", at: 20 * time.Second, span: "3333333333333333"},
		{req: "r4", tool: "send_mail", upstream: "mail", at: 30 * time.Second},
	}
}

func TestAConformingRunGivesNoFindingAndChecksEveryRule(t *testing.T) {
	p := procWith(t)
	res := evaluate(t, supervise.Input{Procedure: p, Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(conforming()...)},
		Sources: []supervise.Source{source(
			ob{id: "obs-1", name: "get_order", span: "1111111111111111"},
			ob{id: "obs-2", name: "issue_refund", span: "aaaaaaaaaaaaaaaa"},
			ob{id: "obs-3", name: "mcp_call", kind: observev1.SubjectKind_SUBJECT_KIND_AGENT,
				span: "3333333333333333", parent: "aaaaaaaaaaaaaaaa"},
			ob{id: "obs-4", name: "gpt", kind: observev1.SubjectKind_SUBJECT_KIND_MODEL, span: "4444444444444444"},
		)}})
	sameFindings(t, res.Findings)
	wantReport := &findingv1alpha1.SuperviseReport{
		SchemaVersion: "0.1", TenantId: tenant, ProjectId: proj, RunId: runID,
		Procedure: &findingv1alpha1.ProcedureRef{ProcedureId: "refund", Version: "1", Digest: p.Digest()},
		Read: &findingv1alpha1.ReadCounts{EventsTaken: 16, EventsLeftOut: map[string]uint64{},
			ObservationsTaken: 2, ObservationsLeftOut: map[string]uint64{"not a tool call": 2}, ExportsWhole: 1},
	}
	for _, id := range []string{"REPEATED_DENIAL", "STEP_OUTSIDE_PROCEDURE", "DEADLINE_EXCEEDED",
		"REQUIRED_STEP_SKIPPED", "STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE"} {
		wantReport.Rules = append(wantReport.Rules, &findingv1alpha1.RuleResult{RuleId: id, RuleVersion: "1",
			State: findingv1alpha1.RuleState_RULE_STATE_CHECKED})
	}
	if !proto.Equal(res.Report, wantReport) {
		t.Fatalf("report\n got %s\nwant %s", protojson.Format(res.Report), protojson.Format(wantReport))
	}
	wantSteps := []supervise.StepMatch{
		{StepID: "lookup", RequestIDs: []string{"r1"}},
		{StepID: "refund", RequestIDs: []string{"r3"}},
		{StepID: "notify", RequestIDs: []string{"r4"}},
	}
	if !reflect.DeepEqual(res.Steps, wantSteps) {
		t.Fatalf("steps %+v, want %+v", res.Steps, wantSteps)
	}
}

func TestOnlyAnOpenedRunsIDIsSupervised(t *testing.T) {
	p := procWith(t)
	for name, id := range map[string]string{
		"a plane's local run": "0190a4c2-7d1e-7b3a-9f00-123456789abc",
		"upper-case hex":      "run-0123456789ABCDEF0123456789ABCDEF",
		"31 digits":           "run-0123456789abcdef0123456789abcde",
		"33 digits":           "run-0123456789abcdef0123456789abcdef0",
		"no prefix":           "0123456789abcdef0123456789abcdef",
		"empty":               "",
	} {
		_, err := supervise.Evaluate(supervise.Input{Procedure: p, Run: supervise.Run{ID: id, Tenant: tenant},
			Exports: []supervise.Export{export(conforming()...)}})
		if !errors.Is(err, supervise.ErrRunID) {
			t.Errorf("%s: Evaluate = %v, want ErrRunID", name, err)
		}
	}
	if _, err := supervise.Evaluate(supervise.Input{Run: supervise.Run{ID: runID, Tenant: tenant}}); !errors.Is(err, supervise.ErrInput) {
		t.Errorf("no procedure: Evaluate = %v, want ErrInput", err)
	}
	if _, err := supervise.Evaluate(supervise.Input{Procedure: p, Run: supervise.Run{ID: runID}}); !errors.Is(err, supervise.ErrInput) {
		t.Errorf("no tenant: Evaluate = %v, want ErrInput", err)
	}
	v02, err := os.ReadFile(filepath.Join("testdata", "procedure-0.2.json"))
	if err != nil {
		t.Fatal(err)
	}
	p02, err := supervise.ReadProcedure(v02)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervise.Evaluate(supervise.Input{Procedure: p02, Run: supervise.Run{ID: runID, Tenant: tenant},
		Exports: []supervise.Export{export(conforming()...)}}); !errors.Is(err, supervise.ErrInput) {
		t.Errorf("a 0.2 procedure: Evaluate = %v, want ErrInput", err)
	}
	twice := []supervise.Source{source(), source()}
	if _, err := supervise.Evaluate(supervise.Input{Procedure: p, Run: supervise.Run{ID: runID, Tenant: tenant},
		Sources: twice}); !errors.Is(err, supervise.ErrInput) {
		t.Errorf("a source twice: Evaluate = %v, want ErrInput", err)
	}
}

func TestARunWithNoEventIsNotAPass(t *testing.T) {
	other := conforming()
	for i := range other {
		other[i].run = "run-ffffffffffffffffffffffffffffffff"
	}
	res := evaluate(t, supervise.Input{Procedure: procWith(t), Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(other...)}})
	sameFindings(t, res.Findings)
	if got := res.Report.GetRead(); got.GetEventsTaken() != 0 || got.GetEventsLeftOut()["another run"] != 16 {
		t.Fatalf("read %v", got)
	}
	for id, state := range rules(res) {
		if state != "RULE_STATE_NOT_CHECKED:no plane event of the run" {
			t.Errorf("%s: %s", id, state)
		}
	}
}

func TestWhatIsNotTheRunsIsLeftOutAndCounted(t *testing.T) {
	calls := append(conforming(),
		call{req: "x1", tool: "wipe_disk", upstream: "ops", at: 40 * time.Second, tenant: "t2"},
		call{req: "x2", tool: "wipe_disk", upstream: "ops", at: 50 * time.Second, run: "run-ffffffffffffffffffffffffffffffff"})
	res := evaluate(t, supervise.Input{Procedure: procWith(t), Exports: []supervise.Export{export(calls...)},
		Sources: []supervise.Source{source(
			ob{id: "obs-1", name: "wipe_disk", tenant: "t2", span: "1000000000000001"},
			ob{id: "obs-2", name: "wipe_disk", basis: observev1.Basis_BASIS_NONE, span: "1000000000000002"},
			ob{id: "obs-3", name: "wipe_disk", run: "run-ffffffffffffffffffffffffffffffff", span: "1000000000000003"},
			ob{id: "obs-4", name: "wipe_disk", proj: "p2", span: "1000000000000004"},
			ob{id: "obs-5", name: "wipe_disk", source: "s9", span: "1000000000000005"},
		)}})
	sameFindings(t, res.Findings)
	read := res.Report.GetRead()
	wantEvents := map[string]uint64{"another tenant": 4, "another run": 4}
	wantObs := map[string]uint64{"another tenant": 1, "basis not claimed": 1, "another run": 1,
		"another project": 1, "another source": 1}
	if read.GetEventsTaken() != 16 || !reflect.DeepEqual(read.GetEventsLeftOut(), wantEvents) ||
		read.GetObservationsTaken() != 0 || !reflect.DeepEqual(read.GetObservationsLeftOut(), wantObs) {
		t.Fatalf("read %v", read)
	}
}

// outside is the conforming run, closed, with a plane call r5 to a tool the
// procedure does not name, and an unjoined observation of another one.
func outside(t *testing.T, src supervise.Source) *supervise.Result {
	t.Helper()
	calls := append(conforming(), call{req: "r5", tool: "wipe_disk", upstream: "ops", at: 40 * time.Second})
	return evaluate(t, supervise.Input{Procedure: procWith(t), Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(calls...)}, Sources: []supervise.Source{src}})
}

func TestAPlaneCallIsConfirmedAndAClaimedObservationOnlySuspected(t *testing.T) {
	p := procWith(t)
	res := outside(t, source(ob{id: "obs-9", name: "drop_table", span: "9999999999999999", at: 45 * time.Second}))
	sameFindings(t, res.Findings,
		want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, confirmed,
			handID(tenant, proj, runID, "refund", "1", "STEP_OUTSIDE_PROCEDURE", "1", "wipe_disk", "ops"),
			evRef("r5-e1", "r5")),
		want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, suspected,
			handID(tenant, proj, runID, "refund", "1", "STEP_OUTSIDE_PROCEDURE", "1", "drop_table"),
			obsRef("s1", "obs-9")))
}

func TestASourceSilentPastItsHeartbeatMakesItsFindingIndeterminate(t *testing.T) {
	p := procWith(t)
	id := handID(tenant, proj, runID, "refund", "1", "STEP_OUTSIDE_PROCEDURE", "1", "drop_table")
	o := ob{id: "obs-9", name: "drop_table", span: "9999999999999999", at: 45 * time.Second}
	// The run's last plane event is r5-e4, at 43 s.
	for name, tc := range map[string]struct {
		src     supervise.Source
		verdict controlv1.FindingVerdict
	}{
		"heard at its heartbeat's edge": {supervise.Source{LastHeard: t0.Add(43*time.Second - 300*time.Second), Heard: true}, suspected},
		"silent one second past it":     {supervise.Source{LastHeard: t0.Add(42*time.Second - 300*time.Second), Heard: true}, indetermin},
		"never heard":                   {supervise.Source{LastHeard: t0.Add(time.Hour)}, indetermin},
	} {
		src := source(o)
		src.LastHeard, src.Heard = tc.src.LastHeard, tc.src.Heard
		res := outside(t, src)
		if len(res.Findings) != 2 {
			t.Fatalf("%s: %s", name, dump(res.Findings))
		}
		sameFindings(t, res.Findings[1:], want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, tc.verdict, id, obsRef("s1", "obs-9")))
	}
}

func TestAnObservationJoinedToAPlaneCallIsThatCall(t *testing.T) {
	res := outside(t, source(
		ob{id: "obs-5", name: "wipe_disk", span: "5555555555555555"},
		ob{id: "obs-7", name: "wipe_disk", span: "7777777777777777"},
		ob{id: "obs-8", name: "mcp_call", kind: observev1.SubjectKind_SUBJECT_KIND_AGENT,
			span: "5555555555555555", parent: "7777777777777777"},
	))
	// r5 carries no span, so nothing joins it until it has the observed one.
	if len(res.Findings) != 2 {
		t.Fatalf("without the plane's span: %s", dump(res.Findings))
	}
	calls := append(conforming(), call{req: "r5", tool: "wipe_disk", upstream: "ops", at: 40 * time.Second, span: "5555555555555555"})
	res = evaluate(t, supervise.Input{Procedure: procWith(t), Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(calls...)}, Sources: []supervise.Source{source(
			ob{id: "obs-5", name: "wipe_disk", span: "5555555555555555"},
			ob{id: "obs-7", name: "wipe_disk", span: "7777777777777777"},
			ob{id: "obs-8", name: "mcp_call", kind: observev1.SubjectKind_SUBJECT_KIND_AGENT,
				span: "5555555555555555", parent: "7777777777777777"},
		)}})
	sameFindings(t, res.Findings, want(procWith(t), "STEP_OUTSIDE_PROCEDURE", medium, alert, confirmed,
		handID(tenant, proj, runID, "refund", "1", "STEP_OUTSIDE_PROCEDURE", "1", "wipe_disk", "ops"),
		evRef("r5-e1", "r5")))
}

func TestTheSameInputGivesTheSameRecords(t *testing.T) {
	in := supervise.Input{Procedure: procWith(t), Run: supervise.Run{ID: runID, Tenant: tenant, Closed: true},
		Exports: []supervise.Export{export(append(conforming()[1:],
			call{req: "r5", tool: "wipe_disk", upstream: "ops", at: 40 * time.Second, outcome: "deny"})...)},
		Sources: []supervise.Source{source(ob{id: "obs-9", name: "drop_table", span: "9999999999999999"})}}
	first, err := supervise.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := supervise.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Findings) < 3 {
		t.Fatalf("the fixture raises too little to compare: %s", dump(first.Findings))
	}
	sameFindings(t, second.Findings, first.Findings...)
	if !proto.Equal(first.Report, second.Report) {
		t.Fatal("two reports of one input differ")
	}
}

func TestAnObservationJoinsOnlyACallOfTheToolItNames(t *testing.T) {
	p := procWith(t, `"observed_as":["get_order"]`, `"observed_as":["fetch_order"]`)
	res := evaluate(t, supervise.Input{Procedure: p, Run: supervise.Run{Closed: true},
		Exports: []supervise.Export{export(conforming()...)}, Sources: []supervise.Source{source(
			ob{id: "obs-1", name: "fetch_order", span: "1111111111111111"},
			ob{id: "obs-2", name: "issue_refund", span: "1111111111111111", at: 25 * time.Second},
			ob{id: "obs-3", name: "drop_table", span: "3333333333333333", at: 26 * time.Second},
		)}})
	sameFindings(t, res.Findings, want(p, "STEP_OUTSIDE_PROCEDURE", medium, alert, suspected,
		handID(tenant, proj, runID, "refund", "1", "STEP_OUTSIDE_PROCEDURE", "1", "drop_table"), obsRef("s1", "obs-3")))
	wantSteps := []supervise.StepMatch{
		{StepID: "lookup", RequestIDs: []string{"r1"}},
		{StepID: "refund", RequestIDs: []string{"r3"}, ObservationIDs: []string{"obs-2"}},
		{StepID: "notify", RequestIDs: []string{"r4"}},
	}
	if !reflect.DeepEqual(res.Steps, wantSteps) {
		t.Fatalf("steps %+v, want %+v", res.Steps, wantSteps)
	}
}
