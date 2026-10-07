//go:build unix

package findinglog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/brand"
)

// exportRecord is one line of an export, with the members these tests read.
type exportRecord struct {
	Type          string          `json:"type"`
	Format        string          `json:"format"`
	Version       string          `json:"version"`
	Source        string          `json:"source"`
	Offset        int64           `json:"offset"`
	Cursor        string          `json:"cursor"`
	Reason        string          `json:"reason"`
	NextCursor    string          `json:"next_cursor"`
	EndReached    bool            `json:"end_reached"`
	TailBytes     int64           `json:"tail_bytes"`
	WriterHeld    bool            `json:"writer_held"`
	Identity      string          `json:"identity"`
	FindingRecord json.RawMessage `json:"finding_record"`
}

func exportQuery(after string, limit int) ExportQuery {
	return ExportQuery{After: after, Limit: limit}
}

// exportLog exports the log in dir and returns its records, failing t on a
// refusal or on output that is not JSON Lines.
func exportLog(t *testing.T, dir string, q ExportQuery) []exportRecord {
	t.Helper()
	var out bytes.Buffer
	if _, err := ExportFile(filepath.Join(dir, FileName), q, &out); err != nil {
		t.Fatalf("ExportFile = %v", err)
	}
	var got []exportRecord
	for _, l := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var r exportRecord
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("an export line that is not JSON: %q: %v", l, err)
		}
		got = append(got, r)
	}
	return got
}

func types(records []exportRecord) string {
	var s []string
	for _, r := range records {
		s = append(s, r.Type)
	}
	return strings.Join(s, " ")
}

func trailerOf(t *testing.T, records []exportRecord) exportRecord {
	t.Helper()
	last := records[len(records)-1]
	if last.Type != "trailer" {
		t.Fatalf("the export does not end with its trailer: %s", types(records))
	}
	return last
}

// findingIDs are the finding ids of the export's finding records, in order.
// Each record carries its log line, which wraps the finding.
func findingIDs(t *testing.T, records []exportRecord) []string {
	t.Helper()
	var ids []string
	for _, r := range records {
		if r.Type != "finding_record" {
			continue
		}
		var line struct {
			FindingRecord struct {
				Finding struct {
					FindingID string `json:"findingId"`
				} `json:"finding"`
			} `json:"findingRecord"`
		}
		mustDo(t, json.Unmarshal(r.FindingRecord, &line))
		ids = append(ids, line.FindingRecord.Finding.FindingID)
	}
	return ids
}

// twoWrites is a log of this package with two writes, idA's and idB's.
func twoWrites(t *testing.T) string {
	t.Helper()
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, finding(idA, confirmed, "repeated_denial"))
	write(t, l, finding(idB, suspected, "outside"))
	mustDo(t, l.Close())
	return dir
}

// lineEnds are the offsets right after each newline of the log in dir.
func lineEnds(t *testing.T, dir string) []int64 {
	t.Helper()
	var ends []int64
	for i, c := range contents(t, filepath.Join(dir, FileName)) {
		if c == '\n' {
			ends = append(ends, int64(i)+1)
		}
	}
	return ends
}

// TestAnExportStartsAfterTheHeader: the log's header is no record, the first
// export starts at the line after it, and the trailer names the identity.
func TestAnExportStartsAfterTheHeader(t *testing.T) {
	dir := twoWrites(t)
	ends := lineEnds(t, dir)
	first := exportLog(t, dir, exportQuery("", 2))
	if got := types(first); got != "header finding_record supervise_report trailer" {
		t.Fatalf("first export: %s", got)
	}
	h, tr := first[0], trailerOf(t, first)
	if h.Format != brand.OTelNamespace+".findings-export" || h.Version != "0.1" || tr.Identity != "log_id" {
		t.Errorf("header %+v, trailer identity %q", h, tr.Identity)
	}
	if first[1].Offset != ends[0] || first[2].Cursor != tr.NextCursor || tr.EndReached {
		t.Errorf("the first finding at %d (the header ends at %d), the report's cursor %q, trailer %+v", first[1].Offset, ends[0], first[2].Cursor, tr)
	}
}

// TestAnExportResumesAtTheNextRecord: each cursor starts the next export at
// the line after it, until the last report.
func TestAnExportResumesAtTheNextRecord(t *testing.T) {
	dir := twoWrites(t)
	ends := lineEnds(t, dir)
	next := exportLog(t, dir, exportQuery(trailerOf(t, exportLog(t, dir, exportQuery("", 2))).NextCursor, 1))
	if ids := findingIDs(t, next); len(ids) != 1 || ids[0] != idB || next[1].Offset != ends[2] {
		t.Errorf("after the cursor: %s with ids %v at %d, want idB at %d", types(next), ids, next[1].Offset, ends[2])
	}
	rest := exportLog(t, dir, exportQuery(trailerOf(t, next).NextCursor, 10))
	if got, tr := types(rest), trailerOf(t, rest); got != "header supervise_report trailer" || !tr.EndReached || tr.TailBytes != 0 {
		t.Errorf("the rest: %s, trailer %+v", got, tr)
	}
}

// TestTheLimitAndTheByteBoundBite: each stops the export at the input where
// leaving it out would read on, and a query outside their range is refused.
func TestTheLimitAndTheByteBoundBite(t *testing.T) {
	dir := twoWrites(t)
	ends := lineEnds(t, dir)
	if got := types(exportLog(t, dir, exportQuery("", 1))); got != "header finding_record trailer" {
		t.Errorf("limit 1: %s", got)
	}
	if got := types(exportLog(t, dir, exportQuery("", 4))); got != "header finding_record supervise_report finding_record supervise_report trailer" {
		t.Errorf("limit 4: %s", got)
	}
	for _, c := range []struct {
		bound int64
		want  string
	}{
		{ends[1], "header finding_record trailer"},
		{ends[1] - 1, "header trailer"},
		{ends[2], "header finding_record supervise_report trailer"},
	} {
		q := exportQuery("", 10)
		q.MaxBytes = c.bound
		if got := types(exportLog(t, dir, q)); got != c.want {
			t.Errorf("max-bytes %d: %s, want %s", c.bound, got, c.want)
		}
	}
	for _, q := range []ExportQuery{{Limit: 0}, {Limit: MaxExportLimit + 1}, {Limit: 1, MaxBytes: -1}} {
		var out bytes.Buffer
		if _, err := ExportFile(filepath.Join(dir, FileName), q, &out); !errors.Is(err, ErrExportQuery) || out.Len() != 0 {
			t.Errorf("%+v: ExportFile = %v with %d bytes written, want ErrExportQuery and nothing", q, err, out.Len())
		}
	}
	if got := types(exportLog(t, dir, exportQuery("", MaxExportLimit))); !strings.HasSuffix(got, "supervise_report trailer") {
		t.Errorf("the largest limit: %s", got)
	}
}

// holdLock takes the writer's exclusive lock on the log in dir, as a writer
// does, without cutting what follows its last report as Open would.
func holdLock(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, FileName)) //nolint:gosec // G304: the test's own file
	mustDo(t, err)
	t.Cleanup(func() { _ = f.Close() })
	mustDo(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
}

// TestAWriteNoReportClosesIsNotExported: whole findings after the last report
// and a torn report are a write still open. None of it is a record; its bytes
// are the trailer's tail, and a gap only when no writer holds the log.
func TestAWriteNoReportClosesIsNotExported(t *testing.T) {
	for _, held := range []bool{false, true} {
		dir := logDir(t)
		l := openLog(t, dir)
		write(t, l, finding(idA, confirmed, "repeated_denial"))
		mustDo(t, l.Close())
		whole := int64(len(contents(t, filepath.Join(dir, FileName))))
		open := line(t, findingRecord(finding(idB, suspected, "outside"))) + line(t, writtenReport(1))[:40]
		appendTo(t, filepath.Join(dir, FileName), open)
		if held {
			holdLock(t, dir)
		}
		got := exportLog(t, dir, exportQuery("", 10))
		tr := trailerOf(t, got)
		if ids := findingIDs(t, got); len(ids) != 1 || ids[0] != idA {
			t.Errorf("held %v: exported ids %v, want idA alone", held, ids)
		}
		if tr.TailBytes != int64(len(open)) || tr.WriterHeld != held || !tr.EndReached {
			t.Errorf("held %v: trailer %+v, want %d tail bytes", held, tr, len(open))
		}
		gap := got[len(got)-2]
		if (gap.Type == "gap" && gap.Reason == "partial_tail" && gap.Offset == whole) == held {
			t.Errorf("held %v: %s, the open write's gap at %d", held, types(got), gap.Offset)
		}
	}
}

// TestALogWithNoHeaderExportsByContent: a log made before the header exports
// whole, and its trailer says its identity is its first line's content.
func TestALogWithNoHeaderExportsByContent(t *testing.T) {
	dir := logDir(t)
	writeFile(t, filepath.Join(dir, FileName), line(t, findingRecord(finding(idA, confirmed, "repeated_denial")))+line(t, writtenReport(1)))
	got := exportLog(t, dir, exportQuery("", 10))
	if ids, tr := findingIDs(t, got), trailerOf(t, got); len(ids) != 1 || ids[0] != idA || tr.Identity != "content" || got[1].Offset != 0 {
		t.Errorf("%s with ids %v, identity %q", types(got), ids, tr.Identity)
	}
	empty := logDir(t)
	writeFile(t, filepath.Join(empty, FileName), "")
	if tr := trailerOf(t, exportLog(t, empty, exportQuery("", 10))); tr.Identity != "none" || tr.NextCursor != "" {
		t.Errorf("an empty log: trailer %+v", tr)
	}
}

// copyLog copies the log in dir to a new directory, its header line, when it
// has one, replaced by header.
func copyLog(t *testing.T, dir, header string) string {
	t.Helper()
	body := string(contents(t, filepath.Join(dir, FileName)))
	if header != "" {
		_, rest, _ := strings.Cut(body, "\n")
		body = header + rest
	}
	other := logDir(t)
	writeFile(t, filepath.Join(other, FileName), body)
	return other
}

func headerWith(t *testing.T, id string) string {
	t.Helper()
	return line(t, &findingv1alpha1.Record{Record: &findingv1alpha1.Record_LogHeader{LogHeader: &findingv1alpha1.LogHeader{
		SchemaVersion: SchemaVersion02, LogId: id,
	}}})
}

// TestACursorOfAnotherLogOrOffALineIsRefused: a cursor holds in its own log
// and in a copy with the same log id, and is refused in another log, in a
// copy with another log id, and off the end of a line.
func TestACursorOfAnotherLogOrOffALineIsRefused(t *testing.T) {
	dir := twoWrites(t)
	cursor := trailerOf(t, exportLog(t, dir, exportQuery("", 2))).NextCursor
	exportLog(t, copyLog(t, dir, ""), exportQuery(cursor, 10))
	offset := strings.Split(cursor, ":")[2]
	n, err := strconv.ParseInt(offset, 10, 64)
	mustDo(t, err)
	for name, c := range map[string]struct {
		dir, cursor string
		want        error
	}{
		"another log":             {twoWrites(t), cursor, ErrCursorOtherLog},
		"a copy, another log id":  {copyLog(t, dir, headerWith(t, "log-"+strings.Repeat("0", 32))), cursor, ErrCursorOtherLog},
		"one byte into the line":  {dir, strings.Replace(cursor, ":"+offset+":", ":"+strconv.FormatInt(n+1, 10)+":", 1), ErrCursorOffLine},
		"one byte before its end": {dir, strings.Replace(cursor, ":"+offset+":", ":"+strconv.FormatInt(n-1, 10)+":", 1), ErrCursorOffLine},
		"another line's end":      {dir, strings.Replace(cursor, ":"+offset+":", ":"+strconv.FormatInt(lineEnds(t, dir)[0], 10)+":", 1), ErrCursorChanged},
		"past the committed end":  {dir, strings.Replace(cursor, ":"+offset+":", ":999999:", 1), ErrCursorPastEnd},
		"not a cursor":            {dir, "v1:" + offset, ErrCursorMalformed},
	} {
		var out bytes.Buffer
		if _, err := ExportFile(filepath.Join(c.dir, FileName), exportQuery(c.cursor, 10), &out); !errors.Is(err, c.want) || out.Len() != 0 {
			t.Errorf("%s: ExportFile = %v with %d bytes written, want %v and nothing", name, err, out.Len(), c.want)
		}
	}
}

// TestACopyOfALogWithNoHeaderIsTheSameLog: with no header, two copies of one
// log are one log to a cursor, and a log with another first line is not.
func TestACopyOfALogWithNoHeaderIsTheSameLog(t *testing.T) {
	dir := logDir(t)
	writeFile(t, filepath.Join(dir, FileName), line(t, findingRecord(finding(idA, confirmed, "repeated_denial")))+line(t, writtenReport(1)))
	cursor := trailerOf(t, exportLog(t, dir, exportQuery("", 1))).NextCursor
	if got := types(exportLog(t, copyLog(t, dir, ""), exportQuery(cursor, 10))); got != "header supervise_report trailer" {
		t.Errorf("a copy: %s", got)
	}
	other := logDir(t)
	writeFile(t, filepath.Join(other, FileName), line(t, findingRecord(finding(idB, confirmed, "repeated_denial")))+line(t, writtenReport(1)))
	if _, err := ExportFile(filepath.Join(other, FileName), exportQuery(cursor, 10), &bytes.Buffer{}); !errors.Is(err, ErrCursorOtherLog) {
		t.Errorf("another first line: ExportFile = %v, want ErrCursorOtherLog", err)
	}
}

// TestTheExportJudgesTheLogAsTheReaderDoes: a log the reader refuses is
// refused with nothing written, and a log a writer holds is read without a
// write lock and left as it was.
func TestTheExportJudgesTheLogAsTheReaderDoes(t *testing.T) {
	damaged := logDir(t)
	writeFile(t, filepath.Join(damaged, FileName), line(t, findingRecord(finding(idA, confirmed, "repeated_denial")))+line(t, writtenReport(2)))
	open := logDir(t)
	writeFile(t, filepath.Join(open, FileName), "")
	mustDo(t, os.Chmod(filepath.Join(open, FileName), 0o640)) //nolint:gosec // G302: the mode under test
	for name, c := range map[string]struct {
		dir  string
		want error
	}{"a report that miscounts": {damaged, ErrDamaged}, "a group-readable file": {open, ErrFileMode}} {
		var out bytes.Buffer
		if _, err := ExportFile(filepath.Join(c.dir, FileName), exportQuery("", 10), &out); !errors.Is(err, c.want) || out.Len() != 0 {
			t.Errorf("%s: ExportFile = %v with %d bytes written, want %v and nothing", name, err, out.Len(), c.want)
		}
	}
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, finding(idA, confirmed, "repeated_denial"))
	before := contents(t, filepath.Join(dir, FileName))
	if tr := trailerOf(t, exportLog(t, dir, exportQuery("", 10))); tr.WriterHeld || tr.TailBytes != 0 {
		t.Errorf("a held log with nothing open: trailer %+v", tr)
	}
	write(t, l, finding(idB, suspected, "outside"))
	if got := contents(t, filepath.Join(dir, FileName)); !bytes.HasPrefix(got, before) {
		t.Error("the export changed the log")
	}
}
