//go:build unix

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/metrics"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
	"github.com/guardana/control/internal/reaction/stopwrite"
)

// unheldRun is a run id in the runs directory's spelling that no record
// holds.
const unheldRun = "run-0123456789abcdef0123456789abcdef"

// floorState is what the route floor's directory and files hold, and their
// modification times.
func floorState(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{"."} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %v\n", name, info.ModTime())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %v\n", e.Name(), info.ModTime())
	}
	return b.String() + floorBytes(t, dir)
}

// ageFloor sets the route floor's directory and files back an hour, so a
// rewrite within the second is seen.
func ageFloor(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorReportsTheRouteAndLeavesItsFloor: doctor names the route, the
// floor, the list and its active stop, the stop's run the runs directory does
// not hold, and the rule whose stops end before a run can; and it reads the
// floor without raising it, leaving its bytes and times as they were.
func TestDoctorReportsTheRouteAndLeavesItsFloor(t *testing.T) {
	tr := newTree(t)
	route := tr.withReaction(t)
	appendStop(t, tr.dir, route, unheldRun)
	floorDir := filepath.Join(tr.dir, "routefloor")
	ageFloor(t, floorDir)
	before := floorState(t, floorDir)
	var stdout, stderr bytes.Buffer
	doctor(context.Background(), tr.config, &stdout, &stderr)
	out := stdout.String()
	for _, want := range []string{
		"ok      reaction      route refunds serial 1 digest " + route.Digest() + ", floor no serial yet",
		"is stopped, read ",
		"; 1 active stop(s); ",
		"stop " + reaction.EntryID("finding-1") + " of run " + unheldRun + " for finding finding-1 under rule " + fixtureRule,
		"stop of run " + unheldRun + ": the runs directory holds no such run",
		"rule " + fixtureRule + " 1 of procedure refund 3 ends a stop after 1h0m0s, before its run can end at 720h0m0s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor does not say %q:\n%s", want, out)
		}
	}
	if after := floorState(t, floorDir); after != before {
		t.Errorf("doctor changed the route floor:\n%s\nwas\n%s", after, before)
	}
}

// TestDoctorFailsARouteBelowItsFloor: what the start refuses, doctor fails.
func TestDoctorFailsARouteBelowItsFloor(t *testing.T) {
	tr := newTree(t)
	route := tr.withReaction(t)
	if _, err := policystate.RaiseRoute(context.Background(), filepath.Join(tr.dir, "routefloor"), fixtureRouteID, 2, route.Digest()); err != nil {
		t.Fatal(err)
	}
	line := doctorLine(t, tr, "fail    reaction ")
	if !strings.Contains(line, "below the floor") {
		t.Errorf("the reaction line is %q, want it to name the floor", line)
	}
}

// TestDoctorSaysAPlaneWithoutARouteStopsNothing is the check's disabled side.
func TestDoctorSaysAPlaneWithoutARouteStopsNothing(t *testing.T) {
	line := doctorLine(t, newTree(t), "ok      reaction ")
	if !strings.Contains(line, "disabled: reaction.route is not set") {
		t.Errorf("the reaction line is %q", line)
	}
}

// TestHealthAnswersTheStopStateAnd503WhenTheListIsRewritten: /healthz names
// the route, the list and its active stops, the stop of a run the runs
// directory does not hold, and the reader's counts; once the list is
// rewritten under the running plane, the state is unknown and the answer is
// 503.
func TestHealthAnswersTheStopStateAnd503WhenTheListIsRewritten(t *testing.T) {
	tr := newTree(t)
	route := tr.withReaction(t)
	p, err := buildServing(t, tr)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	appendStop(t, tr.dir, route, unheldRun)
	p.stops.poller.Poll()
	answer, ok := p.health(p.pauseSource(), time.Now)
	if !ok || answer.Status != "ok" {
		t.Errorf("a readable list with one stop answers %s, ok %v: %v", answer.Status, ok, answer.Problems)
	}
	stoppedAnswer(t, answer.Stops)
	routeAndUsageAnswer(t, answer.Stops, route)

	stops := filepath.Join(tr.dir, "stops")
	if err := os.Remove(filepath.Join(stops, stoplist.FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := stopwrite.Init(context.Background(), stops, route, time.Now()); err != nil {
		t.Fatal(err)
	}
	p.stops.poller.Poll()
	rec := httptest.NewRecorder()
	p.serveHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/healthz over a rewritten list answers %d, want 503", rec.Code)
	}
	answer, _ = p.health(p.pauseSource(), time.Now)
	if answer.Stops.State != "unknown" || answer.Stops.Cause != string(reaction.CauseHeader) {
		t.Errorf("the stop state over a rewritten list is %s (%s), want unknown (%s)", answer.Stops.State, answer.Stops.Cause, reaction.CauseHeader)
	}
}

// stoppedAnswer fails unless s is a list holding one active stop of
// unheldRun, which the runs directory does not hold.
func stoppedAnswer(t *testing.T, s stopsAnswer) {
	t.Helper()
	switch {
	case s.State != "stopped" || s.Active != 1 || len(s.Entries) != 1:
		t.Errorf("stops = %+v", s)
	case s.Entries[0].RunID != unheldRun || s.Entries[0].FindingID != "finding-1" || s.Entries[0].RuleID != fixtureRule:
		t.Errorf("the entry is %+v", s.Entries[0])
	case len(s.UnheldRuns) != 1 || s.UnheldRuns[0] != unheldRun || len(s.UnreadRuns) != 0:
		t.Errorf("unheld %v, unread %v", s.UnheldRuns, s.UnreadRuns)
	}
}

// routeAndUsageAnswer fails unless s names route, its raised floor, a list
// read and its use of its bounds by a header and one stop.
func routeAndUsageAnswer(t *testing.T, s stopsAnswer, route reaction.Route) {
	t.Helper()
	switch {
	case s.Route == nil || *s.Route != (routeAnswer{ID: fixtureRouteID, Serial: 1, Digest: route.Digest()}):
		t.Errorf("route = %+v", s.Route)
	case s.Floor != "serial 1 digest "+route.Digest() || s.ListID == "" || s.AgeMS == nil:
		t.Errorf("floor %q, list %q, age %v", s.Floor, s.ListID, s.AgeMS)
	case s.Usage == nil || s.Usage.Lines != 2 || s.Usage.MaxLines != reaction.MaxListLines || s.Degraded:
		t.Errorf("usage %+v, degraded %v", s.Usage, s.Degraded)
	}
}

// TestHealthIsDegradedPastNineTenthsOfTheListsLines: one line past nine tenths
// of the bound on lines is degraded and still served; at nine tenths it is
// not.
func TestHealthIsDegradedPastNineTenthsOfTheListsLines(t *testing.T) {
	for _, c := range []struct {
		lines    int
		degraded bool
	}{{reaction.MaxListLines * 9 / 10, false}, {reaction.MaxListLines*9/10 + 1, true}} {
		t.Run(fmt.Sprint(c.lines), func(t *testing.T) {
			tr := newTree(t)
			tr.withReaction(t)
			fillList(t, filepath.Join(tr.dir, "stops", stoplist.FileName), c.lines)
			p, err := buildServing(t, tr)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			// Read again now: the start's read of a list this long can be
			// past three poll intervals old by the time the build returns.
			p.stops.poller.Poll()
			answer, ok := p.health(p.pauseSource(), time.Now)
			if !ok || answer.Stops.Degraded != c.degraded || answer.Stops.Usage.Lines != int64(c.lines) {
				t.Fatalf("%d lines: ok %v, stops %+v", c.lines, ok, answer.Stops)
			}
			if want := map[bool]string{true: "degraded", false: "ok"}[c.degraded]; answer.Status != want {
				t.Errorf("%d lines: status %s, want %s", c.lines, answer.Status, want)
			}
		})
	}
}

// fillList appends covered lines to the list at path until it holds lines.
func fillList(t *testing.T, path string, lines int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) //nolint:gosec // G304: the test's own stop list
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Truncate(time.Second)
	var b bytes.Buffer
	for i := 1; i < lines; i++ {
		raw, err := reaction.Covered{FindingID: fmt.Sprintf("f-%05d", i), TenantID: "acme", RunID: unheldRun, CreatedAt: created}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		b.Write(append(raw, '\n'))
	}
	if _, err := f.Write(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestMetricsCountTheStopListsReads: the reader's polls, its failed polls by
// cause and the active stops reach /metrics. A list others may write fails a
// read, and the next read of it made owner-only again is whole.
func TestMetricsCountTheStopListsReads(t *testing.T) {
	tr := newTree(t)
	route := tr.withReaction(t)
	p, err := buildServing(t, tr)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	appendStop(t, tr.dir, route, unheldRun)
	list := filepath.Join(tr.dir, "stops", stoplist.FileName)
	for _, mode := range []os.FileMode{0o666, 0o600} {
		if err := os.Chmod(list, mode); err != nil {
			t.Fatal(err)
		}
		p.stops.poller.Poll()
	}
	body, err := p.metrics(p.pauseSource(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		metrics.Prefix() + "stops_polls_total 3\n",
		metrics.Prefix() + `stops_poll_failures_total{cause="` + string(stoplist.CauseMode) + `"} 1` + "\n",
		metrics.Prefix() + "stops_active_entries 1\n",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics does not hold %q", want)
		}
	}
}
