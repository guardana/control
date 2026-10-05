package main

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/findinglog"
	"github.com/guardana/control/internal/supervise"
)

// superviseLines is what the command prints: the run, what was read and left
// out, each source, each step's instances, each finding with what it rests
// on, each rule's state, each source the run was not checked against, and
// what the log took. Every value an input chose
// goes through oneLine.
func superviseLines(in supervise.Input, res *supervise.Result, unread []string, logged *findinglog.Result) []string {
	r := res.Report
	out := []string{
		"run " + oneLine(r.GetRunId()) + " tenant " + oneLine(r.GetTenantId()) + " project " + oneLine(r.GetProjectId()) +
			" procedure " + oneLine(r.GetProcedure().GetProcedureId()) + " version " + oneLine(r.GetProcedure().GetVersion()),
		"events: " + countedOut(r.GetRead().GetEventsTaken(), r.GetRead().GetEventsLeftOut()),
		"observations: " + countedOut(r.GetRead().GetObservationsTaken(), r.GetRead().GetObservationsLeftOut()),
		fmt.Sprintf("exports: %d whole, %d not whole", r.GetRead().GetExportsWhole(), r.GetRead().GetExportsNotWhole()),
	}
	if blocks := r.GetRead().GetPlaneBlocks(); len(blocks) > 0 {
		var parts []string
		for _, code := range slices.Sorted(maps.Keys(blocks)) {
			parts = append(parts, fmt.Sprintf("%s %d", oneLine(code), blocks[code]))
		}
		out = append(out, "plane blocks, no denial: "+strings.Join(parts, ", "))
	}
	for _, s := range in.Sources {
		out = append(out, sourceSummary(s))
	}
	for _, path := range in.SourcesNotRead {
		out = append(out, "source "+oneLine(path)+": left out, no descriptor at that path")
	}
	for _, s := range res.Steps {
		out = append(out, stepLine(s))
	}
	for _, f := range res.Findings {
		out = append(out, findingLine(f))
	}
	for _, rule := range r.GetRules() {
		out = append(out, supervisedRuleLine(rule))
	}
	for _, line := range unread {
		out = append(out, "not checked: "+line)
	}
	return append(out, loggedLines(logged)...)
}

func loggedLines(logged *findinglog.Result) []string {
	if logged == nil {
		return []string{"findings log: nothing written, no event of the run was read"}
	}
	out := []string{fmt.Sprintf("findings log: %d written, %d already held", logged.Written, logged.Duplicates)}
	for _, id := range logged.Conflicts {
		out = append(out, "findings log: "+oneLine(id)+" held with other content under its verdict, not written")
	}
	return out
}

// countedOut is a count taken, then each reason something was left out with
// its count, in the reasons' order.
func countedOut(taken uint64, leftOut map[string]uint64) string {
	s := strconv.FormatUint(taken, 10) + " taken"
	for _, why := range slices.Sorted(maps.Keys(leftOut)) {
		s += fmt.Sprintf(", %d %s", leftOut[why], oneLine(why))
	}
	return s
}

func sourceSummary(s supervise.Source) string {
	line := "source " + oneLine(s.SourceID) + ": "
	if !s.Heard {
		return line + "never heard"
	}
	noun := "observations"
	if len(s.Observations) == 1 {
		noun = "observation"
	}
	return line + fmt.Sprintf("%d %s, last heard %s", len(s.Observations), noun, s.LastHeard.UTC().Format(time.RFC3339))
}

// stepLine names a step's plane requests, then apart what only the runtime
// reported of it: an observation no plane call joins is no instance of a step.
func stepLine(s supervise.StepMatch) string {
	line := "step " + oneLine(s.StepID) + ": no instance"
	if len(s.RequestIDs) > 0 {
		line = "step " + oneLine(s.StepID) + ": requests " + joinedIDs(s.RequestIDs)
	}
	if len(s.ObservationIDs) > 0 {
		line += "; reported by the runtime only: " + joinedIDs(s.ObservationIDs)
	}
	return line
}

func joinedIDs(ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = oneLine(id)
	}
	return strings.Join(out, ", ")
}

// findingLine is the rule, verdict, escalation and id of a finding, then each
// record it rests on by its ids.
func findingLine(f *findingv1alpha1.FindingRecord) string {
	fd := f.GetFinding()
	line := fmt.Sprintf("finding %s %s %s %s", oneLine(fd.GetRuleId()),
		enumWord(fd.GetVerdict().String(), "FINDING_VERDICT_"), enumWord(f.GetEscalation().String(), "ESCALATION_"),
		oneLine(fd.GetFindingId()))
	var refs []string
	for _, r := range f.GetRefs() {
		switch {
		case r.GetEvent() != nil:
			refs = append(refs, "event "+oneLine(r.GetEvent().GetEventId())+" (request "+oneLine(r.GetEvent().GetRequestId())+")")
		case r.GetObservation() != nil:
			refs = append(refs, "observation "+oneLine(r.GetObservation().GetObservationId())+
				" (source "+oneLine(r.GetObservation().GetSourceId())+")")
		}
	}
	if len(refs) == 0 {
		return line
	}
	return line + " " + strings.Join(refs, ", ")
}

func supervisedRuleLine(r *findingv1alpha1.RuleResult) string {
	line := "rule " + oneLine(r.GetRuleId()) + " "
	switch r.GetState() {
	case findingv1alpha1.RuleState_RULE_STATE_CHECKED:
		return line + "checked"
	case findingv1alpha1.RuleState_RULE_STATE_OFF:
		return line + "off: " + oneLine(r.GetWhy())
	}
	return line + "not checked: " + oneLine(r.GetWhy())
}

// enumWord is an enum value's name without its prefix, in lower case.
func enumWord(name, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}
