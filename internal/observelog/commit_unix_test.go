//go:build unix

package observelog

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
)

func observationLine(t *testing.T, o *observev1.Observation) []byte {
	t.Helper()
	line, err := observe.MarshalLine(&observev1.Record{Record: &observev1.Record_Observation{Observation: o}})
	mustDo(t, err)
	return line
}

func importReportLine(t *testing.T, r *observev1.ImportReport) []byte {
	t.Helper()
	line, err := observe.MarshalLine(&observev1.Record{Record: &observev1.Record_ImportReport{ImportReport: r}})
	mustDo(t, err)
	return line
}

// TestOpenCutsABatchACrashLeftWithoutItsReport: every write ends with its
// report, so the observation of a second batch is cut with whatever of its
// report the crash left, and nothing of it is indexed.
func TestOpenCutsABatchACrashLeftWithoutItsReport(t *testing.T) {
	obsB := string(observationLine(t, observation(spanB, "write_file", "2026-10-04T11:00:00Z")))
	rep2 := importReportLine(t, report("2026-10-04T11:00:00Z"))
	for name, tail := range map[string]string{
		"a torn report":                obsB + string(rep2[:20]),
		"a report without its newline": obsB + string(rep2[:len(rep2)-1]),
		"no report":                    obsB,
	} {
		dir, whole := logWithTail(t, tail)
		path := filepath.Join(dir, FileName)
		kept := contents(t, path)[:whole]
		l := openLog(t, dir)
		if got := contents(t, path); !bytes.Equal(got, kept) {
			t.Errorf("%s: after Open the file holds %d bytes, want the %d of the first batch", name, len(got), whole)
		}
		if got := write(t, l, report("2026-10-04T11:05:00Z"), observation(spanB, "write_file", "2026-10-04T11:00:00Z")); got != (Written{Observations: 1}) {
			t.Errorf("%s: re-importing the cut observation = %+v, want it written", name, got)
		}
	}
}

// TestOpenCutsAFirstBatchACrashLeftWithoutItsReport: no report at all, so
// nothing of the file was reported written.
func TestOpenCutsAFirstBatchACrashLeftWithoutItsReport(t *testing.T) {
	obsA := observationLine(t, observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
	obsB := observationLine(t, observation(spanB, "write_file", "2026-10-04T10:05:00Z"))
	rep := importReportLine(t, report("2026-10-04T10:05:00Z"))
	for name, body := range map[string]string{
		"one observation":                  string(obsA),
		"two observations and a torn tail": string(obsA) + string(obsB) + string(rep[:30]),
		"a torn first line":                string(obsA[:40]),
	} {
		dir := logDir(t)
		writeFile(t, filepath.Join(dir, FileName), body)
		l := openLog(t, dir)
		if got := contents(t, filepath.Join(dir, FileName)); len(got) != 0 {
			t.Errorf("%s: after Open the file holds %q, want nothing", name, got)
		}
		if got := write(t, l, report("2026-10-04T10:06:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z")); got != (Written{Observations: 1}) {
			t.Errorf("%s: re-importing = %+v, want the observation written", name, got)
		}
	}
}

// TestOpenKeepsEveryBatchItsReportCloses: the second batch's report is
// whole, so only the third batch is cut and the second one stays indexed.
func TestOpenKeepsEveryBatchItsReportCloses(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, report("2026-10-04T10:05:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
	write(t, l, report("2026-10-04T10:06:00Z"), observation(spanB, "write_file", "2026-10-04T10:06:00Z"))
	mustDo(t, l.Close())
	path := filepath.Join(dir, FileName)
	kept := contents(t, path)
	other := observation(spanA, "read_file", "2026-10-04T10:05:00Z")
	other.Correlation.SpanId = "1111111111111111"
	other.ObservationId = observe.ObservationID("tenant-a", "project-a", "agent-runtime", traceID, "1111111111111111")
	appendTo(t, path, string(observationLine(t, other)))
	l = openLog(t, dir)
	if got := contents(t, path); !bytes.Equal(got, kept) {
		t.Errorf("after Open the file holds %d bytes, want the %d of two batches", len(got), len(kept))
	}
	got := write(t, l, report("2026-10-04T10:07:00Z"), observation(spanB, "write_file", "2026-10-04T10:07:00Z"), other)
	if got != (Written{Observations: 1, Duplicates: 1}) {
		t.Errorf("Written = %+v, want the second batch's observation a duplicate and the cut one written", got)
	}
}

// TestOpenRefusesABatchNoWriterLeaves: what follows the last report is cut
// only when a writer could have left it, and a refused Open changes nothing.
func TestOpenRefusesABatchNoWriterLeaves(t *testing.T) {
	conflicting := string(observationLine(t, observation(spanA, "delete_file", "2026-10-04T10:05:00Z")))
	obsB := string(observationLine(t, observation(spanB, "write_file", "2026-10-04T10:05:00Z")))
	otherB := string(observationLine(t, observation(spanB, "delete_file", "2026-10-04T10:05:00Z")))
	snake := strings.TrimSuffix(strings.Replace(obsB, `"tenantId"`, `"tenant_id"`, 1), "\n")
	for name, tail := range map[string]string{
		"a whole record not in the writer's form": snake,
		"an id the log holds with other content":  conflicting,
		"one id twice with two contents":          obsB + otherB,
		"a line that is no record":                obsB + "{}\n",
		"a tail that is no line's start":          obsB + "x",
	} {
		dir, whole := logWithTail(t, tail)
		if l, err := Open(dir); !errors.Is(err, ErrDamaged) || l != nil {
			t.Errorf("%s: Open = %v, %v; want ErrDamaged", name, l, err)
		}
		if got := len(contents(t, filepath.Join(dir, FileName))); got != whole+len(tail) {
			t.Errorf("%s: the refused Open changed the file to %d bytes", name, got)
		}
	}
}
