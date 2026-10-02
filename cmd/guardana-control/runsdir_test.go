package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// copyRecord writes a record for id under dir from the record of src, with
// the fields set changes. A record the operator cannot have written, such as
// one already expired, is made this way, as is a directory over the listing's
// bound without opening a thousand runs.
func copyRecord(t *testing.T, dir, src, id string, set map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, src+".run.json")) //nolint:gosec // G304: a file of the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["run_id"] = id
	for k, v := range set {
		fields[k] = v
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".run.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// childID is the i-th run id a test writes by hand.
func childID(i int) string { return fmt.Sprintf("run-%032x", i) }

// TestRunsListStopsAtAThousand: a thousand runs list whole; one more lists the
// first thousand and fails, saying the listing is incomplete.
func TestRunsListStopsAtAThousand(t *testing.T) {
	dir := runsDir(t)
	root := openRun(t, dir, who, "1h")
	for i := 1; i < 1000; i++ {
		copyRecord(t, dir, root.runID, childID(i), map[string]string{"parent": root.runID})
	}
	code, stdout, stderr := invoke(t, "runs", "list", dir)
	if n := strings.Count(stdout, "\n"); code != exitOK || n != 1000 || stderr != "" {
		t.Fatalf("a thousand runs list as %d with %d lines and %q, want 0, 1000 lines and nothing", code, n, stderr)
	}
	copyRecord(t, dir, root.runID, childID(1000), map[string]string{"parent": root.runID})
	code, stdout, stderr = invoke(t, "runs", "list", dir)
	if n := strings.Count(stdout, "\n"); code != exitFail || n != 1000 {
		t.Errorf("1001 runs list as %d with %d lines, want 1 and 1000 lines", code, n)
	}
	if line := oneStderrLine(t, stderr); !strings.HasPrefix(line, brand.CLI+": runs list: the listing is incomplete") || !strings.Contains(line, "1000") {
		t.Errorf("1001 runs: stderr %q, want the listing called incomplete at 1000", line)
	}
}

// TestRunsListReadsExpiryByTheClock: a run past its expiry lists as expired,
// and one closed lists as closed whatever its expiry.
func TestRunsListReadsExpiryByTheClock(t *testing.T) {
	dir := runsDir(t)
	root := openRun(t, dir, who, "1h")
	past := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	times := map[string]string{
		"parent":     root.runID,
		"opened_at":  past.Format(time.RFC3339Nano),
		"expires_at": past.Add(time.Hour).Format(time.RFC3339Nano),
	}
	copyRecord(t, dir, root.runID, childID(1), times)
	times["closed_at"] = past.Add(time.Minute).Format(time.RFC3339Nano)
	copyRecord(t, dir, root.runID, childID(2), times)
	code, stdout, stderr := invoke(t, "runs", "list", dir)
	if code != exitOK || stderr != "" {
		t.Fatalf("list answered %d: %q", code, stderr)
	}
	for id, state := range map[string]string{childID(1): "expired", childID(2): "closed", root.runID: "open"} {
		if !strings.Contains(stdout, id+" "+state+" root "+root.runID+" ") {
			t.Errorf("list %q does not show %s %s", stdout, id, state)
		}
	}
}

// TestRunsRefuseADirectoryThatIsNotOne: a directory holding a file the runs
// directory never writes, one the group may write, and a path with nothing
// at it are refused by each command, and none is changed.
func TestRunsRefuseADirectoryThatIsNotOne(t *testing.T) {
	foreign := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := runsDir(t)
	if err := os.Chmod(shared, 0o770); err != nil { //nolint:gosec // G302: the mode the commands must refuse
		t.Fatal(err)
	}
	expectEveryCommandRefuses(t, foreign, "the directory is not a runs directory")
	expectEveryCommandRefuses(t, shared, "the directory is group- or world-writable")
	expectEveryCommandRefuses(t, filepath.Join(t.TempDir(), "none"), "the directory is not a runs directory")
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 1 {
		t.Errorf("the foreign directory holds %v, %v; want notes.txt alone", entries, err)
	}
	if entries, err := os.ReadDir(shared); err != nil || len(entries) != 0 {
		t.Errorf("the shared directory holds %v, %v; want nothing", entries, err)
	}
}

// TestOnlyRunsOpenMakesAnEmptyDirectoryARunsDirectory: close and list refuse
// an empty directory and leave it empty; open makes it one, and so it does
// with a directory holding only what a crash left while the marker was being
// created.
func TestOnlyRunsOpenMakesAnEmptyDirectoryARunsDirectory(t *testing.T) {
	dir := runsDir(t)
	for _, args := range [][]string{{"runs", "list", dir}, {"runs", "close", dir, childID(1)}} {
		code, stdout, stderr := invoke(t, args...)
		if line := oneStderrLine(t, stderr); code != exitFail || stdout != "" || !strings.Contains(line, "the directory is not a runs directory") {
			t.Errorf("runs %s on an empty directory: exit %d, stdout %q, stderr %q; want 1, not a runs directory", args[1], code, stdout, line)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("runs %s wrote into an empty directory: %v, %v", args[1], entries, err)
		}
	}
	openRun(t, dir, who, "1h")

	crashed := runsDir(t)
	if err := os.WriteFile(filepath.Join(crashed, ".tmp-ABCDEFGHIJKLMNOPQRSTUVWXYZ"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	openRun(t, crashed, who, "1h")
	if code, _, stderr := invoke(t, "runs", "list", crashed); code != exitOK {
		t.Errorf("runs list after a crash leftover: exit %d, %q", code, stderr)
	}
}

// expectEveryCommandRefuses holds open, close and list on dir to one failure
// line under the command's own name, saying want.
func expectEveryCommandRefuses(t *testing.T, dir, want string) {
	t.Helper()
	for _, args := range [][]string{openArgs(dir, who, "1h"), {"runs", "close", dir, childID(1)}, {"runs", "list", dir}} {
		code, stdout, stderr := invoke(t, args...)
		command := "runs " + args[1]
		if line := oneStderrLine(t, stderr); code != exitFail || stdout != "" || !strings.HasPrefix(line, brand.CLI+": "+command+": ") || !strings.Contains(line, want) {
			t.Errorf("%s on %s: exit %d, stdout %q, stderr %q; want 1 and %q", command, dir, code, stdout, line, want)
		}
	}
}

// TestRunsUsageErrors: the wrong number of arguments is a usage error, with
// nothing on stdout; a flagless command answers with the help.
func TestRunsUsageErrors(t *testing.T) {
	dir := runsDir(t)
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"runs alone":        {[]string{"runs"}, helpText()},
		"runs unknown":      {[]string{"runs", "explain", dir}, helpText()},
		"list without dir":  {[]string{"runs", "list"}, helpText()},
		"list with two":     {[]string{"runs", "list", dir, dir}, helpText()},
		"close with one":    {[]string{"runs", "close", dir}, brand.CLI + ": runs close: takes the runs directory and a run id\n"},
		"close with three":  {[]string{"runs", "close", dir, childID(1), childID(2)}, brand.CLI + ": runs close: takes the runs directory and a run id\n"},
		"close with a flag": {[]string{"runs", "close", "--force", dir, childID(1)}, "flag provided but not defined: -force"},
	} {
		code, stdout, stderr := invoke(t, c.args...)
		if code != exitUsage || stdout != "" || !strings.HasPrefix(stderr, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 2 and %q", name, code, stdout, stderr, c.want)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("usage errors changed the directory: %v, %v", entries, err)
	}
}
