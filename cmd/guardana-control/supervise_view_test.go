package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/findinglog"
	"github.com/guardana/control/internal/runview"
)

// deviating is a 0.1 run with a denied refund and a call outside the
// procedure.
func deviating() []supCall {
	return []supCall{
		{req: "r1", tool: "get_order", upstream: "shop"},
		{req: "d1", tool: "issue_refund", upstream: "pay", at: 10 * time.Second, deny: true},
		{req: "w1", tool: "wipe_disk", upstream: "ops", at: 20 * time.Second},
		{req: "r2", tool: "issue_refund", upstream: "pay", at: 30 * time.Second},
	}
}

// superviseInto runs supervise with the findings log in a fresh directory
// of the tree and the extra arguments, and returns its exit and its output.
func (tr supTree) superviseInto(t *testing.T, name string, extra ...string) (int, string, string) {
	t.Helper()
	findings := filepath.Join(tr.dir, name)
	if err := os.Mkdir(findings, 0o700); err != nil {
		t.Fatal(err)
	}
	return invoke(t, append([]string{"supervise", "--procedure", tr.procedure, "--runs", tr.runs, "--run", tr.run,
		"--findings", findings}, extra...)...)
}

func readView(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path in this test's own directory
	if err != nil {
		t.Fatalf("reading the view: %v", err)
	}
	return string(raw)
}

// TestSuperviseViewWritesAnOwnerOnlyPageAndPrintsAsBefore: --view writes
// the run's page, mode 0600, once the findings log is written, and what the
// command prints and exits with are what it prints without --view.
func TestSuperviseViewWritesAnOwnerOnlyPageAndPrintsAsBefore(t *testing.T) {
	tr := newSupTree(t)
	x := tr.export(t, tr.run, supBase, deviating()...)
	tr.closeRun(t, tr.run)
	plainCode, plain, plainErr := tr.superviseInto(t, "plain", "--evidence", x)
	view := filepath.Join(tr.dir, "run.html")
	code, stdout, stderr := tr.superviseInto(t, "viewed", "--evidence", x, "--view", view)
	if code != exitFail || plainCode != exitFail || stderr != "" || plainErr != "" {
		t.Fatalf("exit %d and %d, stderr %q and %q", code, plainCode, stderr, plainErr)
	}
	if stdout != plain {
		t.Errorf("--view changed what is printed:\n%s\nwithout it:\n%s", stdout, plain)
	}
	info, err := os.Stat(view)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the view has mode %04o, want 0600", info.Mode().Perm())
	}
	page := readView(t, view)
	for _, want := range []string{tr.run, `<td class="id"><code>d1</code>`, `<td class="id"><code>w1</code>`,
		"STEP_OUTSIDE_PROCEDURE"} {
		if !strings.Contains(page, want) || !runview.IsPage([]byte(page)) {
			t.Errorf("the view holds no %q", want)
		}
	}
	if records := readLog(t, filepath.Join(tr.dir, "viewed")); len(records) == 0 {
		t.Error("the findings log was not written")
	}
}

// TestSuperviseViewAllDrawsEveryCall: a conforming run's page draws no call
// by default and every call with --view-all; --view replaces a page it drew
// before, owner-only even when the old one was not.
func TestSuperviseViewAllDrawsEveryCall(t *testing.T) {
	tr := newSupTree(t)
	x := tr.export(t, tr.run, supBase, conformingCalls()...)
	tr.closeRun(t, tr.run)
	view := filepath.Join(tr.dir, "run.html")
	if code, _, stderr := tr.superviseInto(t, "first", "--evidence", x, "--view", view); code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if n := strings.Count(readView(t, view), `<tr class="call">`); n != 0 {
		t.Errorf("a run with no finding draws %d calls by default", n)
	}
	if err := os.Chmod(view, 0o644); err != nil { //nolint:gosec // G302: the test widens the old page on purpose
		t.Fatal(err)
	}
	if code, _, stderr := tr.superviseInto(t, "second", "--evidence", x, "--view", view, "--view-all"); code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if n := strings.Count(readView(t, view), `<tr class="call">`); n != 3 {
		t.Errorf("--view-all draws %d calls, want 3", n)
	}
	if info, err := os.Stat(view); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the replaced view: %v, %v", info, err)
	}
}

// TestSuperviseViewReplacesOnlyAPage: a path that holds anything but a page
// it drew, a link, a directory or a path in a missing directory is refused
// before the findings log is opened, and the path is left as it was: its
// type, its mode and what it holds.
func TestSuperviseViewReplacesOnlyAPage(t *testing.T) {
	tr := newSupTree(t)
	x := tr.export(t, tr.run, supBase, deviating()...)
	tr.closeRun(t, tr.run)
	page := filepath.Join(tr.dir, "page.html")
	if code, _, stderr := invoke(t, tr.args("--evidence", x, "--view", page)...); code != exitFail || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	link := filepath.Join(tr.dir, "link.html")
	if err := os.Symlink(page, link); err != nil {
		t.Fatal(err)
	}
	notAPage := filepath.Join(tr.dir, "notes.html")
	writeFixture(t, notAPage, "<!DOCTYPE html>\n<p>my notes</p>\n")
	for name, path := range map[string]string{
		"the procedure": tr.procedure, "a page of other text": notAPage, "a link to a page": link,
		"a directory": tr.findings, "a missing directory": filepath.Join(tr.dir, "none", "run.html"),
		"the findings log": filepath.Join(tr.findings, findinglog.FileName),
	} {
		t.Run(name, func(t *testing.T) {
			before := pathState(t, path)
			findings := filepath.Join(tr.dir, "log-"+strings.ReplaceAll(name, " ", "-"))
			if err := os.Mkdir(findings, 0o700); err != nil {
				t.Fatal(err)
			}
			supRefused(t, []string{"supervise", "--procedure", tr.procedure, "--runs", tr.runs, "--run", tr.run,
				"--findings", findings, "--evidence", x, "--view", path}, "--view")
			if after := pathState(t, path); after != before {
				t.Errorf("the refused path changed from %q to %q", before, after)
			}
			if _, err := os.Stat(filepath.Join(findings, findinglog.FileName)); !os.IsNotExist(err) {
				t.Errorf("the findings log was written: %v", err)
			}
		})
	}
	for _, args := range [][]string{{"--view-all"}, {"--view", page, "--view", page}} {
		code, stdout, stderr := invoke(t, append(tr.args("--evidence", x), args...)...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "--view") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
}

// pathState is what path is without following a link: its type and mode,
// then a file's bytes, a link's target or a directory's names, or "absent".
// Any other error reading it fails the test.
func pathState(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	var held string
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		held, err = os.Readlink(path)
	case info.IsDir():
		var entries []fs.DirEntry
		entries, err = os.ReadDir(path)
		for _, e := range entries {
			held += e.Name() + "\n"
		}
	default:
		var raw []byte
		raw, err = os.ReadFile(path) //nolint:gosec // G304: a path in this test's own directory
		held = string(raw)
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().String() + "\n" + held
}
