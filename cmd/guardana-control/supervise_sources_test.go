package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
)

// TestSuperviseSaysWhatOfASourceItRead: a descriptor that does not exist is
// left out and one whose log directory holds no log yet was never heard;
// neither is a refused input, and neither is a pass.
func TestSuperviseSaysWhatOfASourceItRead(t *testing.T) {
	tr := newSupTree(t)
	descriptor, log := tr.source(t)
	x := tr.export(t, tr.run, supBase, conformingCalls()...)
	tr.closeRun(t, tr.run)
	code, stdout, stderr := invoke(t, tr.args("--evidence", x, "--source", descriptor, "--log", log)...)
	if want := "source agent-runtime: never heard\n"; code != 1 || stderr != "" || !strings.Contains(stdout, want) ||
		!strings.HasSuffix(stdout, "not checked: source agent-runtime never heard\nfindings log: 0 written, 0 already held\n") {
		t.Errorf("exit %d, stderr %q, stdout\n%s\nwant 1, %q and the source not checked", code, stderr, stdout, want)
	}
	if err := os.Remove(descriptor); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = invoke(t, tr.args("--evidence", x, "--source", descriptor, "--log", log)...)
	if want := "source " + descriptor + ": left out, no descriptor at that path\n"; code != 1 || stderr != "" ||
		!strings.Contains(stdout, want) || !strings.Contains(stdout, "not checked: source "+descriptor+" absent\n") {
		t.Errorf("exit %d, stderr %q, stdout\n%s\nwant 1, %q and the source not checked", code, stderr, stdout, want)
	}
}

// TestSuperviseASilentSourceIsNotAPass: a conforming run whose source was
// heard within its heartbeat of the run's last plane event exits 0; the same
// run dated past the source's import finds it silent and exits 1, naming it.
func TestSuperviseASilentSourceIsNotAPass(t *testing.T) {
	for name, c := range map[string]struct {
		base time.Time
		code int
	}{
		"heard":                 {supBase, 0},
		"dated past its import": {time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second), 1},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newSupTree(t)
			descriptor, log := tr.source(t)
			tr.observe(t, descriptor, log, "search_docs", c.base.Add(5*time.Second))
			x := tr.export(t, tr.run, c.base, conformingCalls()...)
			tr.closeRun(t, tr.run)
			code, stdout, stderr := invoke(t, tr.args("--evidence", x, "--source", descriptor, "--log", log)...)
			if code != c.code || stderr != "" || strings.Contains(stdout, "finding ") {
				t.Fatalf("exit %d, stderr %q, stdout\n%s\nwant %d and no finding", code, stderr, stdout, c.code)
			}
			if silent := strings.Contains(stdout, "not checked: source agent-runtime silent\n"); silent != (c.code == 1) {
				t.Errorf("stdout names the source silent: %v, want %v:\n%s", silent, c.code == 1, stdout)
			}
		})
	}
}

// TestSuperviseJudgesASourceHeardAsOfTheRunsLastEvent: a call the source
// claims for the run and no step lists is suspected while the source was
// heard within its heartbeat of the run's last plane event. A source is heard
// no later than its import report was received, so events dated after that
// by more than the heartbeat find it silent, and the finding indeterminate.
func TestSuperviseJudgesASourceHeardAsOfTheRunsLastEvent(t *testing.T) {
	for name, c := range map[string]struct {
		base          time.Time
		verdict, last string
	}{
		"heard":                 {supBase, "suspected", "last heard 2026-01-05T09:00:05Z"},
		"dated past its import": {time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second), "indeterminate", "last heard "},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newSupTree(t)
			descriptor, log := tr.source(t)
			tr.observe(t, descriptor, log, "search_web", c.base.Add(5*time.Second))
			x := tr.export(t, tr.run, c.base, conformingCalls()...)
			tr.closeRun(t, tr.run)
			code, stdout, stderr := invoke(t, tr.args("--evidence", x, "--source", descriptor, "--log", log)...)
			if code != 1 || stderr != "" {
				t.Fatalf("exit %d, stderr %q, stdout\n%s", code, stderr, stdout)
			}
			finding := regexp.MustCompile(`(?m)^finding STEP_OUTSIDE_PROCEDURE ` + c.verdict +
				` alert fnd-[0-9a-f]{32} observation obs-[0-9a-f]{32} \(source agent-runtime\)$`)
			if !finding.MatchString(stdout) || strings.Count(stdout, "finding ") != 1 {
				t.Errorf("stdout holds no one %s STEP_OUTSIDE_PROCEDURE line citing the observation:\n%s", c.verdict, stdout)
			}
			if want := "source agent-runtime: 1 observation, " + c.last; !strings.Contains(stdout, want) {
				t.Errorf("stdout does not hold %q:\n%s", want, stdout)
			}
		})
	}
}

// TestSuperviseReadsNoEventOfALineCutShort: an export whose last line has no
// newline was cut short; the line is not read, the export is not whole, and
// the rules that rest on what was not seen are not checked.
func TestSuperviseReadsNoEventOfALineCutShort(t *testing.T) {
	tr := newSupTree(t)
	x := tr.export(t, tr.run, supBase, conformingCalls()...)
	raw, err := os.ReadFile(x) //nolint:gosec // G304: a path in this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	writeFixture(t, x, strings.Join(lines[:len(lines)-1], "\n"))
	tr.closeRun(t, tr.run)
	code, stdout, stderr := invoke(t, tr.args("--evidence", x)...)
	if code != 1 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, stdout\n%s", code, stderr, stdout)
	}
	for _, want := range []string{"events: 11 taken\n", "exports: 0 whole, 1 not whole\n",
		"rule REQUIRED_STEP_SKIPPED not checked: an export is not whole: it has no trailer: it was cut short\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not hold %q:\n%s", want, stdout)
		}
	}
}

// TestSuperviseAnAbsentDescriptorPutsAbsenceInDoubt: a required step the run
// skipped is a suspected finding with no source named, and an indeterminate
// one when a named source's descriptor is absent, since what that source did
// not report was not read.
func TestSuperviseAnAbsentDescriptorPutsAbsenceInDoubt(t *testing.T) {
	for name, c := range map[string]struct {
		absent  bool
		verdict string
	}{
		"no source named":      {false, "suspected"},
		"an absent descriptor": {true, "indeterminate"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newSupTree(t)
			x := tr.export(t, tr.run, supBase, supCall{req: "r1", tool: "get_order", upstream: "shop"})
			tr.closeRun(t, tr.run)
			args := tr.args("--evidence", x)
			if c.absent {
				args = append(args, "--source", filepath.Join(tr.dir, "gone.json"), "--log", tr.dir)
			}
			code, stdout, stderr := invoke(t, args...)
			if code != 1 || stderr != "" {
				t.Fatalf("exit %d, stderr %q, stdout\n%s", code, stderr, stdout)
			}
			want := regexp.MustCompile(`(?m)^finding REQUIRED_STEP_SKIPPED ` + c.verdict + ` alert fnd-[0-9a-f]{32}`)
			if !want.MatchString(stdout) {
				t.Errorf("stdout holds no %s REQUIRED_STEP_SKIPPED finding:\n%s", c.verdict, stdout)
			}
		})
	}
}

// TestSuperviseAnUnjoinedObservationIsNoInstanceOfItsStep: a call of a step's
// tool that only the runtime reported is printed as reported by the runtime
// only, never as an instance of the step.
func TestSuperviseAnUnjoinedObservationIsNoInstanceOfItsStep(t *testing.T) {
	tr := newSupTree(t)
	descriptor, log := tr.source(t)
	tr.observe(t, descriptor, log, "issue_refund", supBase.Add(5*time.Second))
	x := tr.export(t, tr.run, supBase, supCall{req: "r1", tool: "get_order", upstream: "shop"})
	tr.closeRun(t, tr.run)
	_, stdout, stderr := invoke(t, tr.args("--evidence", x, "--source", descriptor, "--log", log)...)
	want := regexp.MustCompile(`(?m)^step refund: no instance; reported by the runtime only: obs-[0-9a-f]{32}$`)
	if !want.MatchString(stdout) || strings.Contains(stdout, "; observations ") {
		t.Errorf("stderr %q, stdout\n%s\nwant the refund step's observation reported by the runtime only", stderr, stdout)
	}
}

// twoSpans is one OTLP/JSON line holding supSpan's span twice, one trace id
// and one span id, as a call of first and then of second, ending at end.
func (tr supTree) twoSpans(first, second string, end time.Time) string {
	line := func(tool string) string {
		return fmt.Sprintf(supSpan, end.Add(-time.Second).UnixNano(), end.UnixNano(), tool, tr.run)
	}
	a, b := line(first), line(second)
	open, closing := `"spans":[`, `]}],"schemaUrl"`
	stop := strings.Index(a, closing)
	return a[:stop] + "," + b[strings.Index(b, open)+len(open):strings.Index(b, closing)] + a[stop:]
}

// TestSuperviseASourceWhoseLogRefusedAConflictIsNotAPass: an agent that
// sends an allowed call and then, under the same trace and span id, a call
// no step lists has its log keep the first and count the second a conflict.
// The run is not judged as if the log held all it was sent: the rules that
// read the source's reports are not checked and supervise exits 1. The same
// run whose source was sent the allowed call alone exits 0.
func TestSuperviseASourceWhoseLogRefusedAConflictIsNotAPass(t *testing.T) {
	const why = `not checked: source "agent-runtime" is in doubt: an import found an observation id with two contents`
	for name, c := range map[string]struct {
		second          string
		conflicts, code int
	}{"the allowed call alone": {"", 0, exitOK}, "a forbidden call after it": {"drop_table", 1, exitFail}} {
		t.Run(name, func(t *testing.T) {
			tr := newSupTree(t)
			writeFixture(t, tr.procedure, supProcedure02)
			descriptor, log := tr.source(t)
			input := filepath.Join(tr.dir, "spans.jsonl")
			body := fmt.Sprintf(supSpan, supBase.Add(4*time.Second).UnixNano(), supBase.Add(5*time.Second).UnixNano(), "search_docs", tr.run)
			if c.second != "" {
				body = tr.twoSpans("search_docs", c.second, supBase.Add(5*time.Second))
			}
			writeFixture(t, input, body)
			code, out, stderr := invoke(t, "observe", "import", "--source", descriptor, "--log", log, input)
			if counted(t, out, "conflict") != c.conflicts || code != c.code {
				t.Fatalf("import: exit %d, stdout %q, stderr %q; want %d conflict(s)", code, out, stderr, c.conflicts)
			}
			x := tr.export(t, tr.run, supBase, conformingCalls()...)
			tr.closeRun(t, tr.run)
			code, stdout, stderr := invoke(t, tr.args("--evidence", x, "--source", descriptor, "--log", log)...)
			if code != c.code || stderr != "" || strings.Contains(stdout, "finding ") {
				t.Fatalf("exit %d, stderr %q, stdout\n%s\nwant %d and no finding", code, stderr, stdout, c.code)
			}
			for _, rule := range []string{"STEP_OUTSIDE_PROCEDURE", "DENIED_ACTION_RETRIED_AROUND"} {
				want := "rule " + rule + " checked\n"
				if c.second != "" {
					want = "rule " + rule + " " + why + "\n"
				}
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout does not hold %q:\n%s", want, stdout)
				}
			}
		})
	}
}

// TestSuperviseSumsTheConflictsOfEveryImportReport: the conflicts of each
// report add up whatever descriptor wrote it, and two counts whose sum would
// wrap round to none stop at the largest.
func TestSuperviseSumsTheConflictsOfEveryImportReport(t *testing.T) {
	reported := func(project string, n uint64) *observev1.Record {
		return &observev1.Record{Record: &observev1.Record_ImportReport{ImportReport: &observev1.ImportReport{
			ProjectId: project, Counts: &observev1.ImportCounts{Conflict: n}}}}
	}
	seen := &observev1.Record{Record: &observev1.Record_Observation{Observation: &observev1.Observation{}}}
	for name, c := range map[string]struct {
		records []*observev1.Record
		want    uint64
	}{
		"none":                {[]*observev1.Record{seen, reported(supProject, 0)}, 0},
		"two descriptors":     {[]*observev1.Record{reported(supProject, 2), seen, reported("elsewhere", 3)}, 5},
		"a sum past the most": {[]*observev1.Record{reported(supProject, 1<<63), reported(supProject, 1<<63)}, math.MaxUint64},
	} {
		if got := conflictsOf(c.records); got != c.want {
			t.Errorf("%s: %d conflicts, want %d", name, got, c.want)
		}
	}
}
