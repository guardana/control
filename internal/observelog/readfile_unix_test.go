//go:build unix

package observelog

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
	"google.golang.org/protobuf/proto"
)

func observationRecord(o *observev1.Observation) *observev1.Record {
	return &observev1.Record{Record: &observev1.Record_Observation{Observation: o}}
}

func reportRecord(r *observev1.ImportReport) *observev1.Record {
	return &observev1.Record{Record: &observev1.Record_ImportReport{ImportReport: r}}
}

// line is r as the codec writes it, newline included.
func line(t *testing.T, r *observev1.Record) string {
	t.Helper()
	b, err := observe.MarshalLine(r)
	mustDo(t, err)
	return string(b)
}

func sameRecords(t *testing.T, got, want []*observev1.Record) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ReadFile returned %d records, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("record %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestReadFileReturnsWhatTheWriterWroteWhileItHoldsTheLog: the read takes no
// lock and writes nothing, so the writer goes on writing after it.
func TestReadFileReturnsWhatTheWriterWroteWhileItHoldsTheLog(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	a := observation(spanA, "read_file", "2026-10-04T10:05:00Z")
	b := observation(spanB, "write_file", "2026-10-04T10:05:00Z")
	first, second := report("2026-10-04T10:05:00Z"), report("2026-10-04T10:06:00Z")
	write(t, l, first, a, b)
	write(t, l, second, a)
	path := filepath.Join(dir, FileName)
	before := contents(t, path)

	got, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	counted := &observev1.ImportCounts{Read: 2, Duplicate: 1}
	secondAsWritten := &observev1.ImportReport{SchemaVersion: "0.1", TenantId: "tenant-a", ProjectId: "project-a",
		Source: second.GetSource(), ReceivedTime: second.GetReceivedTime(), Counts: counted}
	sameRecords(t, got, []*observev1.Record{observationRecord(a), observationRecord(b), reportRecord(first), reportRecord(secondAsWritten)})
	if !bytes.Equal(contents(t, path), before) {
		t.Error("ReadFile changed the log file")
	}
	write(t, l, report("2026-10-04T10:07:00Z"))
}

func TestReadFileOfALogWithNoWriteIsNoRecords(t *testing.T) {
	dir := logDir(t)
	mustDo(t, openLogAndClose(dir))
	got, err := ReadFile(filepath.Join(dir, FileName))
	if err != nil || len(got) != 0 {
		t.Errorf("ReadFile = %v, %v; want no records and no error", got, err)
	}
}

// TestReadFileLeavesOutATornLastLine: what follows the last newline is a line
// still being written, whether it is the start of one or all of it but its
// newline.
func TestReadFileLeavesOutATornLastLine(t *testing.T) {
	a := observation(spanA, "read_file", "2026-10-04T10:05:00Z")
	first := report("2026-10-04T10:05:00Z")
	whole := strings.TrimSuffix(line(t, reportRecord(report("2026-10-04T10:06:00Z"))), "\n")
	for _, tail := range []string{`{"observation":{"schemaVersion":"0.1"`, `{"`, whole} {
		dir := logDir(t)
		write(t, openLog(t, dir), first, a)
		path := filepath.Join(dir, FileName)
		appendTo(t, path, tail)
		got, err := ReadFile(path)
		if err != nil {
			t.Fatalf("tail %.20q: ReadFile = %v", tail, err)
		}
		sameRecords(t, got, []*observev1.Record{observationRecord(a), reportRecord(first)})
	}
}

// TestReadFileRefusesATailNoWriterLeaves: bytes after the last newline that
// cannot begin a line the codec writes are damage, not a write in progress.
func TestReadFileRefusesATailNoWriterLeaves(t *testing.T) {
	dir := logDir(t)
	write(t, openLog(t, dir), report("2026-10-04T10:05:00Z"))
	path := filepath.Join(dir, FileName)
	appendTo(t, path, "garbage")
	if got, err := ReadFile(path); !errors.Is(err, ErrDamaged) || got != nil {
		t.Errorf("ReadFile = %v, %v; want ErrDamaged", got, err)
	}
}

// TestReadFileLeavesOutAWriteItsReportHasNotClosed: observations after the
// last import report belong to a write not yet committed.
func TestReadFileLeavesOutAWriteItsReportHasNotClosed(t *testing.T) {
	a := observation(spanA, "read_file", "2026-10-04T10:05:00Z")
	b := observation(spanB, "write_file", "2026-10-04T10:06:00Z")
	first := report("2026-10-04T10:05:00Z")

	dir := logDir(t)
	write(t, openLog(t, dir), first, a)
	path := filepath.Join(dir, FileName)
	appendTo(t, path, line(t, observationRecord(b)))
	got, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	sameRecords(t, got, []*observev1.Record{observationRecord(a), reportRecord(first)})

	alone := filepath.Join(logDir(t), FileName)
	writeFile(t, alone, line(t, observationRecord(a))+line(t, observationRecord(b)))
	if got, err := ReadFile(alone); err != nil || len(got) != 0 {
		t.Errorf("a log with no report: ReadFile = %v, %v; want no records and no error", got, err)
	}
}

// TestReadFileNamesADamagedLineByItsOffsetAndNeverItsBytes: the codec's own
// complaint quotes the member it refused, so the error must not carry it.
func TestReadFileNamesADamagedLineByItsOffsetAndNeverItsBytes(t *testing.T) {
	closing := report("2026-10-04T10:06:00Z")
	other := observation(spanA, "planted_tool", "2026-10-04T10:06:00Z")
	for name, c := range map[string]struct{ damage, after string }{
		"an unknown member, committed":   {`{"observation":{"planted":"x"}}` + "\n", line(t, reportRecord(closing))},
		"an unknown member, uncommitted": {`{"observation":{"planted":"x"}}` + "\n", ""},
		"a carriage return":              {strings.Replace(line(t, reportRecord(closing)), "\n", "\r\n", 1), ""},
		"an id with other content":       {line(t, observationRecord(other)), line(t, reportRecord(closing))},
	} {
		dir := logDir(t)
		l := openLog(t, dir)
		write(t, l, report("2026-10-04T10:05:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
		mustDo(t, l.Close())
		path := filepath.Join(dir, FileName)
		offset := len(contents(t, path))
		appendTo(t, path, c.damage+c.after)

		got, err := ReadFile(path)
		if !errors.Is(err, ErrDamaged) || got != nil {
			t.Errorf("%s: ReadFile = %v, %v; want ErrDamaged", name, got, err)
			continue
		}
		if want := fmt.Sprintf("line 3, at byte %d", offset); !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %q does not name %q", name, err, want)
		}
		if strings.Contains(err.Error(), "planted") {
			t.Errorf("%s: %q carries the line's bytes", name, err)
		}
	}
}

// TestReadFileRefusesALineOverTheWritersBound: a line of exactly the bound is
// read and judged, one byte more is refused before it is judged.
func TestReadFileRefusesALineOverTheWritersBound(t *testing.T) {
	closing := line(t, reportRecord(report("2026-10-04T10:05:00Z")))
	for _, c := range []struct {
		length int
		over   bool
	}{{observe.MaxLineBytes, false}, {observe.MaxLineBytes + 1, true}} {
		path := filepath.Join(logDir(t), FileName)
		writeFile(t, path, strings.Repeat("x", c.length)+"\n"+closing)
		_, err := ReadFile(path)
		if !errors.Is(err, ErrDamaged) {
			t.Fatalf("%d bytes: ReadFile = %v, want ErrDamaged", c.length, err)
		}
		if over := strings.Contains(err.Error(), "is over 65536 bytes"); over != c.over {
			t.Errorf("%d bytes: %q, want refused as over the bound: %v", c.length, err, c.over)
		}
		if !strings.Contains(err.Error(), "line 1, at byte 0") {
			t.Errorf("%d bytes: %q does not name line 1 at byte 0", c.length, err)
		}
	}
}

// TestReadFileRefusesWhatIsNoRegularFile: a link is refused even when it names
// the log itself, and a dangling one is not a log never written.
func TestReadFileRefusesWhatIsNoRegularFile(t *testing.T) {
	dir := logDir(t)
	write(t, openLog(t, dir), report("2026-10-04T10:05:00Z"))
	link, dangling, fifo := filepath.Join(dir, "link"), filepath.Join(dir, "dangling"), filepath.Join(dir, "fifo")
	mustDo(t, os.Symlink(FileName, link))
	mustDo(t, os.Symlink("nothing.jsonl", dangling))
	mustDo(t, syscall.Mkfifo(fifo, 0o600))
	for _, path := range []string{link, dangling, dir, fifo} {
		if got, err := ReadFile(path); !errors.Is(err, ErrNotRegular) || got != nil {
			t.Errorf("%s: ReadFile = %v, %v; want ErrNotRegular", path, got, err)
		}
	}
}

func TestReadFileRefusesALogFileTheGroupOrOthersMayReach(t *testing.T) {
	for _, mode := range []fs.FileMode{0o640, 0o620, 0o610, 0o604, 0o602, 0o601} {
		dir := logDir(t)
		write(t, openLog(t, dir), report("2026-10-04T10:05:00Z"))
		path := filepath.Join(dir, FileName)
		mustDo(t, os.Chmod(path, mode))
		if got, err := ReadFile(path); !errors.Is(err, ErrFileMode) || got != nil {
			t.Errorf("mode %04o: ReadFile = %v, %v; want ErrFileMode", mode, got, err)
		}
	}
}

func TestReadFileRefusesALogFileOfAnotherAccount(t *testing.T) {
	dir := logDir(t)
	write(t, openLog(t, dir), report("2026-10-04T10:05:00Z"))
	wantOwner(t, func(fs.FileInfo) int { return os.Geteuid() + 1 })
	if got, err := ReadFile(filepath.Join(dir, FileName)); !errors.Is(err, ErrOwner) || got != nil {
		t.Errorf("ReadFile = %v, %v; want ErrOwner", got, err)
	}
}

func TestReadFileRefusesALogFileWithASecondName(t *testing.T) {
	dir := logDir(t)
	write(t, openLog(t, dir), report("2026-10-04T10:05:00Z"))
	mustDo(t, os.Link(filepath.Join(dir, FileName), filepath.Join(dir, "second")))
	if got, err := ReadFile(filepath.Join(dir, FileName)); !errors.Is(err, ErrLinks) || got != nil {
		t.Errorf("ReadFile = %v, %v; want ErrLinks", got, err)
	}
}

// TestReadFileRefusesALogLargerThanTheWriterOpens: both files are sparse and
// hold no newline, so the one at the bound is refused as damaged, after the
// bound let it through.
func TestReadFileRefusesALogLargerThanTheWriterOpens(t *testing.T) {
	for _, c := range []struct {
		size int64
		want error
	}{{1 << 30, ErrDamaged}, {1<<30 + 1, ErrTooLarge}} {
		path := filepath.Join(logDir(t), FileName)
		writeFile(t, path, "")
		mustDo(t, os.Truncate(path, c.size))
		_, err := ReadFile(path)
		if !errors.Is(err, c.want) || (errors.Is(c.want, ErrDamaged) && errors.Is(err, ErrTooLarge)) {
			t.Errorf("%d bytes: ReadFile = %v, want %v", c.size, err, c.want)
		}
	}
}

// TestReadFileOfNoFileIsNotExist: the caller tells a log never written from
// one it cannot read by this error, and the read creates nothing.
func TestReadFileOfNoFileIsNotExist(t *testing.T) {
	dir := logDir(t)
	path := filepath.Join(dir, FileName)
	if got, err := ReadFile(path); !errors.Is(err, fs.ErrNotExist) || got != nil {
		t.Errorf("ReadFile = %v, %v; want fs.ErrNotExist", got, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile left a file: %v", err)
	}
	if _, err := ReadFile(filepath.Join(dir, "absent", FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing directory: ReadFile = %v, want fs.ErrNotExist", err)
	}
}

func TestReadFileOfAnEmptyFileIsNoRecords(t *testing.T) {
	path := filepath.Join(logDir(t), FileName)
	writeFile(t, path, "")
	if got, err := ReadFile(path); err != nil || len(got) != 0 {
		t.Errorf("ReadFile = %v, %v; want no records and no error", got, err)
	}
}
