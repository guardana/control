//go:build unix

package findinglog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
)

var (
	errCrash = errors.New("the process died here")
	errStuck = errors.New("cannot truncate")
)

// crashAt is a write that reaches the disk only up to the byte where cut
// puts it in the buffer the writer hands it, and a cut back that fails, as a
// process that dies mid-write leaves the file.
func crashAt(cut func(buf []byte) int) fileOps {
	return fileOps{
		write: func(f *os.File, b []byte) (int, error) {
			n, err := f.Write(b[:cut(b)])
			return n, errors.Join(errCrash, err)
		},
		sync:     (*os.File).Sync,
		truncate: func(*os.File, int64) error { return errStuck },
	}
}

func reportStart(b []byte) int { return bytes.Index(b, []byte(`{"superviseReport":`)) }

// TestAReopenedLogHoldsExactlyTheCommittedWrites: the second write dies at
// each point before its report reached the file whole, so the open cuts all
// of it, the read never returns it, and writing it again appends it.
func TestAReopenedLogHoldsExactlyTheCommittedWrites(t *testing.T) {
	for name, cut := range map[string]func([]byte) int{
		"inside the first finding":            func([]byte) int { return 30 },
		"between two findings":                func(b []byte) int { return bytes.IndexByte(b, '\n') + 1 },
		"between the findings and the report": reportStart,
		"inside the report":                   func(b []byte) int { return reportStart(b) + 40 },
		"before the report's newline":         func(b []byte) int { return len(b) - 1 },
	} {
		dir := logDir(t)
		l := openLog(t, dir)
		path := filepath.Join(dir, FileName)
		write(t, l, finding(idA, confirmed, "repeated_denial"))
		before := contents(t, path)
		l.ops = crashAt(cut)
		second := []*findingv1alpha1.FindingRecord{finding(idB, suspected, "outside"), finding(idA, suspected, "repeated_denial")}
		if _, err := l.Write(second, report()); !errors.Is(err, ErrWrite) || !errors.Is(err, errCrash) || !errors.Is(err, errStuck) {
			t.Errorf("%s: Write = %v, want ErrWrite carrying both causes", name, err)
		}
		if got := contents(t, path); len(got) == len(before) {
			t.Fatalf("%s: the crash left nothing to cut, so the case examines nothing", name)
		}
		if _, err := l.Write(nil, report()); !errors.Is(err, ErrFailed) {
			t.Errorf("%s: a later Write = %v, want ErrFailed", name, err)
		}
		sameRecords(t, name+", read under the writer", readBack(t, dir), findingRecord(finding(idA, confirmed, "repeated_denial")), writtenReport(1))
		mustDo(t, l.Close())
		l = openLog(t, dir)
		if got := contents(t, path); !bytes.Equal(got, before) {
			t.Errorf("%s: after Open the file holds %d bytes, want the %d of the first write", name, len(got), len(before))
		}
		if got := write(t, l, second...); got.Written != 2 || got.Duplicates != 0 {
			t.Errorf("%s: writing the cut findings again = %+v, want both written", name, got)
		}
	}
}

// TestTheReportReachingTheFileWholeCommitsTheWrite: the write dies at its
// sync, after the report and its newline, and cannot be cut back; the open
// keeps it, so its findings are duplicates from then on.
func TestTheReportReachingTheFileWholeCommitsTheWrite(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	l.ops = fileOps{
		write:    (*os.File).Write,
		sync:     func(*os.File) error { return errCrash },
		truncate: func(*os.File, int64) error { return errStuck },
	}
	if _, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idA, confirmed, "repeated_denial")}, report()); !errors.Is(err, ErrWrite) {
		t.Fatalf("Write = %v, want ErrWrite", err)
	}
	mustDo(t, l.Close())
	l = openLog(t, dir)
	if got := write(t, l, finding(idA, confirmed, "repeated_denial")); got.Written != 0 || got.Duplicates != 1 {
		t.Errorf("writing the finding again = %+v, want a duplicate", got)
	}
	sameRecords(t, "the log", readBack(t, dir), findingRecord(finding(idA, confirmed, "repeated_denial")), writtenReport(1), writtenReport(0))
}

// TestAFailedWriteIsCutBackAndLeavesTheIndexAsItWas: the cut back works, so
// the log goes on taking writes.
func TestAFailedWriteIsCutBackAndLeavesTheIndexAsItWas(t *testing.T) {
	for name, ops := range map[string]fileOps{
		"write":       {write: func(f *os.File, b []byte) (int, error) { n, _ := f.Write(b[:len(b)/2]); return n, errCrash }, sync: (*os.File).Sync, truncate: (*os.File).Truncate},
		"short write": {write: func(f *os.File, b []byte) (int, error) { return f.Write(b[:len(b)-1]) }, sync: (*os.File).Sync, truncate: (*os.File).Truncate},
		"sync":        {write: (*os.File).Write, sync: func(*os.File) error { return errCrash }, truncate: (*os.File).Truncate},
	} {
		dir := logDir(t)
		l := openLog(t, dir)
		path := filepath.Join(dir, FileName)
		write(t, l, finding(idA, confirmed, "repeated_denial"))
		before := contents(t, path)
		l.ops = ops
		_, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idB, suspected, "outside")}, report())
		if !errors.Is(err, ErrWrite) || (!errors.Is(err, errCrash) && !errors.Is(err, io.ErrShortWrite)) {
			t.Errorf("%s: Write = %v, want ErrWrite carrying the cause", name, err)
		}
		if got := contents(t, path); !bytes.Equal(got, before) {
			t.Errorf("%s: the failed write left %q", name, got[len(before):])
		}
		l.ops = osOps
		if got := write(t, l, finding(idB, suspected, "outside")); got.Written != 1 {
			t.Errorf("%s: the failed write indexed the finding: %+v", name, got)
		}
	}
}

// TestAFileChangedUnderTheWriterRefusesEveryLaterWrite: one change lands
// before the write and is seen by the check before it, the other during it
// and is seen only by the check after the sync.
func TestAFileChangedUnderTheWriterRefusesEveryLaterWrite(t *testing.T) {
	for name, c := range map[string]struct {
		during bool
		change func(t *testing.T, path string)
	}{
		"renamed before":  {false, func(t *testing.T, path string) { mustDo(t, os.Rename(path, path+".old")) }},
		"extended before": {false, func(t *testing.T, path string) { appendTo(t, path, "{") }},
		"renamed during":  {true, func(t *testing.T, path string) { mustDo(t, os.Rename(path, path+".old")) }},
		"extended during": {true, func(t *testing.T, path string) { appendTo(t, path, "{") }},
	} {
		dir := logDir(t)
		l := openLog(t, dir)
		path := filepath.Join(dir, FileName)
		if c.during {
			l.ops.write = func(f *os.File, b []byte) (int, error) {
				n, err := f.Write(b)
				c.change(t, path)
				return n, err
			}
		} else {
			c.change(t, path)
		}
		if _, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idA, confirmed, "repeated_denial")}, report()); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: Write = %v, want ErrChanged", name, err)
		}
		l.ops = osOps
		if _, err := l.Write(nil, report()); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: the next Write = %v, want ErrChanged", name, err)
		}
	}
}
