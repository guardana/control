package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// secondPlane writes a copy of the tree's plane in mode, without its
// overrides when unclassified, and returns its path.
func (tr coverageTree) secondPlane(t *testing.T, name, mode string, unclassified bool) string {
	t.Helper()
	body := strings.Replace(coveragePlane, "mode: ENFORCE", "mode: "+mode, 1)
	if unclassified {
		body = body[:strings.Index(body, "overrides:")]
	}
	path := filepath.Join(tr.dir, name)
	writeFixture(t, path, body)
	return path
}

// wholeExport writes an unfiltered export that reached the end of its trail,
// holds no proposal, and spans an hour either side of now, and returns its
// path.
func (tr coverageTree) wholeExport(t *testing.T, name string) string {
	t.Helper()
	event := func(id string, at time.Time) string {
		return fmt.Sprintf(`{"type":"event","offset":0,"cursor":"c","event":{"eventId":%q,"kind":"EVENT_KIND_POLICY_DECIDED",`+
			`"occurredAt":%q,"schemaVersion":"1.0"}}`, id, at.UTC().Format(time.RFC3339Nano))
	}
	path := filepath.Join(tr.dir, name)
	writeFixture(t, path, strings.Join([]string{
		fmt.Sprintf(`{"type":"header","format":%q,"version":"1.0","file":"gateway.trail",`+
			`"source":"57f7f9c13a3299681c3a7122a448585ec8e5984f4c854f0c3dd54b08c997e2a3","query":{"limit":1000}}`,
			brand.OTelNamespace+".evidence-export"),
		event("e1", time.Now().Add(-time.Hour)),
		event("e2", time.Now().Add(time.Hour)),
		`{"type":"trailer","next_cursor":"c","end_reached":true,"tail_bytes":0,"writer_held":false,` +
			`"counts":{"event":2,"gap":0,"duplicate":0},"scanned_bytes":0,"dedup_scope":"export"}`,
	}, "\n")+"\n")
	return path
}

// TestCoverageCountsAnObservePlaneWithoutAnOverride: a plane in OBSERVE that
// lists the path's upstream lets the unclassified call run, so the path is
// decided, not enforced, though another plane classifies and enforces it.
func TestCoverageCountsAnObservePlaneWithoutAnOverride(t *testing.T) {
	tr := newCoverageTree(t, "")
	tr.importFresh(t)
	observing := tr.secondPlane(t, "observe.yaml", "OBSERVE", true)
	code, stdout, stderr := invoke(t, tr.args("--plane", tr.plane, "--plane", observing)...)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
	}
	lines := outputLines(stdout)
	if want := "orders.read-file decided, not enforced: planes " + tr.plane + ", " + observing; lines[0] != want {
		t.Errorf("first line %q, want %q", lines[0], want)
	}
	if want := "  plane " + observing + ": decided, not enforced: an unclassified call runs in OBSERVE;"; !strings.HasPrefix(lines[2], want) {
		t.Errorf("second plane line %q, want one starting %q", lines[2], want)
	}
}

// TestCoverageBindsEachExportToItsPlane: an --evidence is the export of the
// --plane before it. A call is around the planes only when every plane in
// front of the path has its export; otherwise the line names the plane
// whose export is missing.
func TestCoverageBindsEachExportToItsPlane(t *testing.T) {
	tr := newCoverageTree(t, "")
	tr.importFresh(t)
	second := tr.secondPlane(t, "second.yaml", "ENFORCE", false)
	xa, xb := tr.wholeExport(t, "a.jsonl"), tr.wholeExport(t, "b.jsonl")
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"the second plane without an export", []string{"--plane", tr.plane, "--evidence", xa, "--plane", second},
			0, "  join not checked: 1 (no export of plane " + second + ")\n"},
		{"the first plane without an export", []string{"--plane", tr.plane, "--plane", second, "--evidence", xb},
			0, "  join not checked: 1 (no export of plane " + tr.plane + ")\n"},
		{"both planes with an export", []string{"--plane", tr.plane, "--evidence", xa, "--plane", second, "--evidence", xb},
			1, "  join: 1 around the plane\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := invoke(t, tr.args(tc.args...)...)
			if code != tc.code || stderr != "" || !strings.Contains(stdout, tc.want) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q in:\n%s", code, stderr, tc.code, tc.want, stdout)
			}
		})
	}
}

// TestCoverageRefusesAnExportWithoutItsPlane: an --evidence before any
// --plane, or a second one for a plane, belongs to no plane.
func TestCoverageRefusesAnExportWithoutItsPlane(t *testing.T) {
	tr := newCoverageTree(t, "")
	x := tr.wholeExport(t, "a.jsonl")
	refusedCoverage(t, tr.args("--evidence", x, "--plane", tr.plane), "--evidence", "--plane")
	refusedCoverage(t, tr.args("--plane", tr.plane, "--evidence", x, "--evidence", x), "--evidence", tr.plane)
}

// TestCoverageSaysASourceWasNotGiven: a source the inventory names that no
// --source was given for says so, and does not claim an absent descriptor.
func TestCoverageSaysASourceWasNotGiven(t *testing.T) {
	tr := newCoverageTree(t, "")
	code, stdout, stderr := invoke(t, "coverage", "--inventory", tr.inventory)
	if code != 1 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
	}
	if want := "  source agent-runtime: not covered: no --source given for it"; outputLines(stdout)[1] != want {
		t.Errorf("source line %q, want %q", outputLines(stdout)[1], want)
	}
}

// TestCoverageRefusesAnExportAnotherAccountCouldWrite: an export the group or
// others could write is refused as the inventory is, and no map is printed.
func TestCoverageRefusesAnExportAnotherAccountCouldWrite(t *testing.T) {
	for _, mode := range []os.FileMode{0o666, 0o620, 0o602} {
		t.Run(mode.String(), func(t *testing.T) {
			tr := newCoverageTree(t, "")
			x := tr.wholeExport(t, "export.jsonl")
			if err := os.Chmod(x, mode); err != nil {
				t.Fatal(err)
			}
			refusedCoverage(t, tr.args("--plane", tr.plane, "--evidence", x), "--evidence", x, "mode")
		})
	}
}
