package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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
