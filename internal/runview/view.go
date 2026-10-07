package runview

import (
	"bytes"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"slices"

	"github.com/guardana/control/internal/supervise"
)

// Bounds of one page.
const (
	// MaxCalls bounds the calls one page draws, in either view, and the
	// findings it lists.
	MaxCalls = 1000
	// MaxText bounds the characters one string an input chose is drawn
	// with; an escaped character counts as one.
	MaxText = 80
	// MaxPageBytes bounds a page, and the file a writer may take for one.
	MaxPageBytes = 16 << 20
	// maxListed bounds the numbers one cell lists, of calls or findings.
	maxListed = 20
)

// header starts every page, so a writer can tell a page this package drew
// from any other file before replacing it.
const header = "<!DOCTYPE html>\n<!-- run view 1 -->\n"

// Options choose what a page draws.
type Options struct {
	// All draws every call up to MaxCalls instead of the neighbourhood of
	// each finding: the calls it cites and the calls just before and after.
	All bool
}

//go:embed page.tmpl
var files embed.FS

var page = template.Must(template.ParseFS(files, "page.tmpl"))

// ErrTooLarge is a page over MaxPageBytes, which no writer takes.
var ErrTooLarge = errors.New("runview: the page is over its size bound")

// Page draws res, a supervision of a run against p.
func Page(p *supervise.Procedure, res *supervise.Result, opt Options) ([]byte, error) {
	if p == nil || res == nil || res.Report == nil {
		return nil, errors.New("runview: no procedure or no supervision to draw")
	}
	m := build(p, res, opt)
	var b bytes.Buffer
	b.WriteString(header)
	if err := page.Execute(&b, m); err != nil {
		return nil, fmt.Errorf("runview: %w", err)
	}
	if b.Len() > MaxPageBytes {
		return nil, ErrTooLarge
	}
	return b.Bytes(), nil
}

// IsPage reports whether b starts as a page this package draws.
func IsPage(b []byte) bool { return bytes.HasPrefix(b, []byte(header)) }

// row is one drawn call, or a gap of calls not drawn when Gap is set.
type row struct {
	Gap      string
	N        int
	Mark     markCell
	ID       text
	Tool     text
	Upstream text
	Step     *text
	Run      text
	When     string
	Clock    string
	Export   int
	Source   *text
	Outcome  string
	Approval string
	Resource *resource
	Reasons  list
	Findings nums
}

type resource struct{ Type, ID, Tenant, Environment text }

// list is numbers or strings in a cell, the first maxListed of them, and
// how many more there were.
type list struct {
	Items []text
	More  int
}

func listOf(items []string) list {
	var l list
	for i, s := range items {
		if i == maxListed {
			l.More = len(items) - i
			break
		}
		l.Items = append(l.Items, textOf(s))
	}
	return l
}

// order is the places of res.Instances as the page draws them: by time,
// those with none last, and otherwise as supervise read them.
func order(ins []supervise.Instance) []int {
	idx := make([]int, len(ins))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		x, y := ins[a], ins[b]
		switch {
		case x.Timed != y.Timed && x.Timed:
			return -1
		case x.Timed != y.Timed:
			return 1
		case x.Timed:
			return x.At.Compare(y.At)
		}
		return 0
	})
	return idx
}

// drawn picks the positions the page draws and says how many it left out
// past the bound.
func drawn(n int, rests [][]int, pos []int, all bool) ([]int, int) {
	keep := make([]bool, n)
	for p := range keep {
		keep[p] = all
	}
	for _, r := range rests {
		for _, i := range r {
			for _, p := range []int{pos[i] - 1, pos[i], pos[i] + 1} {
				if p >= 0 && p < n {
					keep[p] = true
				}
			}
		}
	}
	var out []int
	for p, k := range keep {
		if !k {
			continue
		}
		if len(out) == MaxCalls {
			return out, n - out[len(out)-1] - 1
		}
		out = append(out, p)
	}
	return out, 0
}

// rowsOf draws the calls at the positions picked, with a gap row for each
// run of calls between them and after the last.
func rowsOf(res *supervise.Result, idx, picked []int, short int, findingsOf map[int][]int) []row {
	var out []row
	prev := -1
	for _, p := range picked {
		if p > prev+1 {
			out = append(out, row{Gap: notDrawn(p - prev - 1)})
		}
		out = append(out, callRow(p+1, res.Instances[idx[p]], findingsOf[idx[p]]))
		prev = p
	}
	switch rest := len(idx) - prev - 1; {
	case short > 0:
		out = append(out, row{Gap: notDrawn(short) + fmt.Sprintf(": the page stops at %d calls", MaxCalls)})
	case rest > 0 && len(picked) > 0:
		out = append(out, row{Gap: notDrawn(rest)})
	}
	return out
}

func notDrawn(n int) string {
	if n == 1 {
		return "1 call not drawn"
	}
	return fmt.Sprintf("%d calls not drawn", n)
}

func callRow(n int, in supervise.Instance, findings []int) row {
	r := row{N: n, Mark: markOf(in), Tool: textOf(in.Tool), Upstream: textOf(in.Upstream), Run: textOf(in.Run),
		Export: in.Export + 1, Outcome: cmp.Or(outcomeWords[in.Outcome], "outcome unknown"), Approval: approvalWords[in.Approval],
		Reasons: listOf(in.Reasons), Findings: numbered("F", findings)}
	r.ID = textOf(in.Request)
	if in.Observation != "" {
		r.ID, r.Export = textOf(in.Observation), 0
		src := textOf(in.Source)
		r.Source = &src
	}
	if in.Step != "" {
		s := textOf(in.Step)
		r.Step = &s
	}
	if in.Resource != (supervise.Resource{}) {
		r.Resource = &resource{textOf(in.Resource.Type), textOf(in.Resource.ID), textOf(in.Resource.Tenant),
			textOf(in.Resource.Environment)}
	}
	r.When, r.Clock = "no time", "order not told"
	if in.Timed {
		r.When = in.At.UTC().Format("2006-01-02T15:04:05.999999999Z")
		switch {
		case in.Observation != "":
			r.Clock = "the source's clock, not ordered against the planes'"
		case in.Told:
			r.Clock = ""
		}
	}
	return r
}

var outcomeWords = map[supervise.Outcome]string{
	supervise.OutcomeUnknown: "outcome unknown", supervise.OutcomeOpen: "open", supervise.OutcomeCompleted: "completed",
	supervise.OutcomeFailed: "failed", supervise.OutcomeDenied: "denied", supervise.OutcomeBlocked: "blocked by the plane",
}

var approvalWords = map[supervise.Approval]string{
	supervise.ApprovalNone: "", supervise.ApprovalAsked: "approval asked, not granted",
	supervise.ApprovalGranted: "approval granted",
}
