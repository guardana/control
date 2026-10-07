//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/findinglog"
)

// The dispatch test's tables live beside the commands they were written for;
// the export keeps the evidence export's statuses, 2 for nothing written.
// findingsLog is a findings log a writer of findinglog created and closed,
// holding its header alone.
func findingsLog(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "findings")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := findinglog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runFindingsExport(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	status := run(append([]string{"findings", "export"}, args...), &stdout, &stderr)
	return status, stdout.String(), stderr.String()
}

// appendBytes appends s to the file at path, as a writer cut short leaves it.
func appendBytes(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // G304: the test's own file
	if err == nil {
		_, err = f.WriteString(s)
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		t.Fatal(err)
	}
}

// lastRecord is the last JSON line of an export.
func lastRecord(t *testing.T, out string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	var r map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		t.Fatalf("the last line is not JSON: %q", out)
	}
	return r
}

// TestFindingsExportExitsByWhatTheExportHolds: 0 for a whole export with no
// gap, 1 for a whole export holding one, its trailer still written.
func TestFindingsExportExitsByWhatTheExportHolds(t *testing.T) {
	dir := findingsLog(t)
	status, out, errOut := runFindingsExport("--findings", dir)
	if tr := lastRecord(t, out); status != exitOK || errOut != "" || tr["type"] != "trailer" || tr["identity"] != "log_id" {
		t.Errorf("a log with its header alone: status %d, stderr %q, stdout %q", status, errOut, out)
	}
	if !strings.HasPrefix(out, `{"type":"header","format":"`+brand.OTelNamespace+`.findings-export","version":"0.1",`) {
		t.Errorf("the export does not open with its header: %q", out)
	}
	appendBytes(t, filepath.Join(dir, findinglog.FileName), `{"findingRecord":{`)
	status, out, errOut = runFindingsExport("--findings", dir)
	if tr := lastRecord(t, out); status != exitFail || tr["type"] != "trailer" || tr["tail_bytes"] != float64(18) ||
		!strings.Contains(errOut, "1 gap(s)") {
		t.Errorf("a write no writer finishes: status %d, stderr %q, stdout %q", status, errOut, out)
	}
}

// TestFindingsExportRefusesWritingNothing: a usage error, a query out of
// range and a cursor of another log exit 2 with nothing on standard output.
func TestFindingsExportRefusesWritingNothing(t *testing.T) {
	dir, other := findingsLog(t), findingsLog(t)
	_, out, _ := runFindingsExport("--findings", other)
	cursor, _ := lastRecord(t, out)["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("no cursor to try: %q", out)
	}
	for name, args := range map[string][]string{
		"no --findings":        {},
		"--findings twice":     {"--findings", dir, "--findings", dir},
		"an argument":          {"--findings", dir, dir},
		"limit 0":              {"--findings", dir, "--limit", "0"},
		"limit past the bound": {"--findings", dir, "--limit", "100001"},
		"a negative bound":     {"--findings", dir, "--max-bytes", "-1"},
		"another log's cursor": {"--findings", dir, "--after", cursor},
	} {
		status, out, errOut := runFindingsExport(args...)
		if status != exitUsage || out != "" || !strings.HasPrefix(errOut, brand.CLI+": findings export: ") {
			t.Errorf("%s: status %d, stdout %q, stderr %q", name, status, out, errOut)
		}
	}
	if status, _, errOut := runFindingsExport("--findings", dir, "--limit", "100000"); status != exitOK {
		t.Errorf("the largest limit: status %d, stderr %q", status, errOut)
	}
}
