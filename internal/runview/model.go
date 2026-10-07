package runview

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/supervise"
)

// model is everything page.tmpl draws.
type model struct {
	Run, Tenant, Project       text
	Procedure, Version, Digest text
	Children                   string
	Tree                       []member
	Read                       []string
	Blocks                     []count
	Steps                      []stepRow
	Findings                   []findingRow
	FindingsLeftOut            int
	All                        bool
	Rows                       []row
	Total, Drawn, Short        int
	NotSeen                    []notSeen
	Rules                      []ruleRow
	Legend                     []legendRow
	NoCallCited, NoCall        bool
}

type member struct {
	Run    text
	Parent *text
}

type count struct {
	What text
	N    uint64
}

type stepRow struct {
	ID, Tool, Upstream text
	Required           bool
	Calls              nums
	Mark               *markCell
}

type findingRow struct {
	N                   int
	Rule, ID, Run       text
	Verdict, Escalation string
	Calls               nums
	LeftOut             uint64
	NamesRun            bool
}

type notSeen struct {
	Mark markCell
}

type ruleRow struct {
	Rule  text
	State string
	Why   text
}

func build(p *supervise.Procedure, res *supervise.Result, opt Options) model {
	r := res.Report
	m := model{Run: textOf(r.GetRunId()), Tenant: textOf(r.GetTenantId()), Project: textOf(r.GetProjectId()),
		Procedure: textOf(r.GetProcedure().GetProcedureId()), Version: textOf(r.GetProcedure().GetVersion()),
		Digest: textOf(r.GetProcedure().GetDigest()), All: opt.All, Legend: legend}
	m.tree(r)
	m.read(r.GetRead())
	idx := order(res.Instances)
	pos := make([]int, len(idx))
	for p, i := range idx {
		pos[i] = p
	}
	findingsOf := m.findings(res, pos)
	rests := res.Rests
	if len(rests) > MaxCalls {
		rests = rests[:MaxCalls]
	}
	picked, short := drawn(len(idx), rests, pos, opt.All)
	m.Rows = rowsOf(res, idx, picked, short, findingsOf)
	m.Total, m.Drawn, m.Short = len(idx), len(picked), short
	m.NoCall = len(idx) == 0
	m.NoCallCited = !opt.All && len(picked) == 0 && len(idx) > 0
	m.steps(p, res.Instances, pos)
	m.notSeen(res)
	for _, rule := range r.GetRules() {
		m.Rules = append(m.Rules, ruleRow{Rule: textOf(rule.GetRuleId()), Why: textOf(rule.GetWhy()),
			State: enumWord(rule.GetState().String(), "RULE_STATE_")})
	}
	return m
}

func (m *model) tree(r *findingv1alpha1.SuperviseReport) {
	switch r.GetChildren() {
	case findingv1alpha1.ChildrenMode_CHILDREN_MODE_INHERIT:
		m.Children = "inherit: every run of the tree is judged"
	case findingv1alpha1.ChildrenMode_CHILDREN_MODE_SEPARATE:
		m.Children = "separate: the run's own calls are judged, its children are listed"
	}
	for _, mb := range r.GetRunTree() {
		x := member{Run: textOf(mb.GetRunId())}
		if mb.GetParentRunId() != "" {
			parent := textOf(mb.GetParentRunId())
			x.Parent = &parent
		}
		m.Tree = append(m.Tree, x)
	}
}

func (m *model) read(c *findingv1alpha1.ReadCounts) {
	m.Read = []string{
		"events: " + countedOut(c.GetEventsTaken(), c.GetEventsLeftOut()),
		"observations: " + countedOut(c.GetObservationsTaken(), c.GetObservationsLeftOut()),
		fmt.Sprintf("exports: %d whole, %d not whole", c.GetExportsWhole(), c.GetExportsNotWhole()),
	}
	blocks := c.GetPlaneBlocks()
	for _, code := range slices.Sorted(maps.Keys(blocks)) {
		m.Blocks = append(m.Blocks, count{What: textOf(code), N: blocks[code]})
	}
}

// countedOut is a count taken and how many were left out, by how many
// reasons; the reasons' words are in the findings log's report.
func countedOut(taken uint64, out map[string]uint64) string {
	var left uint64
	for _, n := range out {
		left += n
	}
	return fmt.Sprintf("%d taken, %d left out", taken, left)
}

// findings lists each finding with the positions of the calls it cites,
// and returns for each call the findings that cite it.
func (m *model) findings(res *supervise.Result, pos []int) map[int][]int {
	citing := map[int][]int{}
	namesRuns := len(res.Report.GetRunTree()) > 0
	for i, f := range res.Findings {
		if i == MaxCalls {
			m.FindingsLeftOut = len(res.Findings) - i
			break
		}
		fd := f.GetFinding()
		row := findingRow{N: i + 1, Rule: textOf(fd.GetRuleId()), ID: textOf(fd.GetFindingId()), Run: textOf(fd.GetRunId()),
			Verdict: enumWord(fd.GetVerdict().String(), "FINDING_VERDICT_"), NamesRun: namesRuns,
			Escalation: enumWord(f.GetEscalation().String(), "ESCALATION_"), LeftOut: f.GetRefsLeftOut()}
		var calls []int
		if i < len(res.Rests) {
			for _, n := range res.Rests[i] {
				calls = append(calls, pos[n]+1)
				citing[n] = append(citing[n], i+1)
			}
		}
		slices.Sort(calls)
		row.Calls = numbered("#", calls)
		m.Findings = append(m.Findings, row)
	}
	return citing
}

// nums is call or finding numbers in a cell, the first maxListed of them,
// and how many more there were.
type nums struct {
	Items []string
	More  int
}

func numbered(prefix string, ns []int) nums {
	var l nums
	for i, n := range ns {
		if i == maxListed {
			l.More = len(ns) - i
			break
		}
		l.Items = append(l.Items, prefix+strconv.Itoa(n))
	}
	return l
}

// steps lists the procedure's steps in order with the calls of each, and
// marks a step with none as not seen.
func (m *model) steps(p *supervise.Procedure, ins []supervise.Instance, pos []int) {
	for _, s := range p.Steps() {
		var calls []int
		for i, in := range ins {
			if in.Step == s.ID {
				calls = append(calls, pos[i]+1)
			}
		}
		slices.Sort(calls)
		row := stepRow{ID: textOf(s.ID), Tool: textOf(s.Tool), Upstream: textOf(s.Upstream), Required: s.Required,
			Calls: numbered("#", calls)}
		if len(calls) == 0 {
			c := cell(markNotSeen, "no call of this step")
			row.Mark = &c
		}
		m.Steps = append(m.Steps, row)
	}
}

// notSeen names each source the run was not checked against.
func (m *model) notSeen(res *supervise.Result) {
	add := func(names []string, post string) {
		for _, n := range names {
			src := textOf(n)
			m.NotSeen = append(m.NotSeen, notSeen{markCell{Mark: markNotSeen, Label: labels[markNotSeen],
				Pre: "source ", Code: &src, Post: post}})
		}
	}
	add(res.SourcesNotRead, ": not read, no descriptor at that path")
	add(res.NeverHeard, ": never heard")
	add(res.Silent, ": silent, its heartbeat ran out before the run's last event")
}

func enumWord(name, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}
