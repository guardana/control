//go:build unix

package observelog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
)

// TestOneOpenLogRemembersWhatItWrote: the index the open built does not
// hold the first write's id, so only what the write added can find it.
func TestOneOpenLogRemembersWhatItWrote(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, report("2026-10-04T10:05:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
	if got := write(t, l, report("2026-10-04T10:06:00Z"), observation(spanA, "read_file", "2026-10-04T10:06:00Z")); got != (Written{Duplicates: 1}) {
		t.Errorf("the same observation again = %+v, want a duplicate", got)
	}
	if got := write(t, l, report("2026-10-04T10:07:00Z"), observation(spanA, "delete_file", "2026-10-04T10:05:00Z")); got != (Written{Conflicts: 1}) {
		t.Errorf("the same id with other content = %+v, want a conflict", got)
	}
	if n := bytes.Count(contents(t, filepath.Join(dir, FileName)), []byte(`{"observation":`)); n != 1 {
		t.Errorf("the log holds %d observations, want 1", n)
	}
}

// TestAFileChangedDuringTheWriteIsRefused: the change happens between the
// check before the write and the write's end, so only the check after the
// sync sees it.
func TestAFileChangedDuringTheWriteIsRefused(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, path string){
		"renamed":  func(t *testing.T, path string) { mustDo(t, os.Rename(path, path+".old")) },
		"extended": func(t *testing.T, path string) { appendTo(t, path, "{") },
	} {
		dir := logDir(t)
		l := openLog(t, dir)
		path := filepath.Join(dir, FileName)
		l.ops.write = func(f *os.File, b []byte) (int, error) {
			n, err := f.Write(b)
			change(t, path)
			return n, err
		}
		if _, err := l.Write([]*observev1.Observation{observation(spanA, "read_file", "2026-10-04T10:05:00Z")}, report("2026-10-04T10:05:00Z")); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: Write = %v, want ErrChanged", name, err)
		}
		l.ops = osOps
		if _, err := l.Write(nil, report("2026-10-04T10:06:00Z")); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: the next Write = %v, want ErrChanged", name, err)
		}
	}
}

// TestAWriteThatCannotBeCutBackRefusesEveryLaterOne: the write fails half
// way and so does the cut back; the next open cuts what the write left.
func TestAWriteThatCannotBeCutBackRefusesEveryLaterOne(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	path := filepath.Join(dir, FileName)
	write(t, l, report("2026-10-04T10:05:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
	before := contents(t, path)
	failing, stuck := errors.New("disk full"), errors.New("cannot truncate")
	l.ops = fileOps{
		write:    func(f *os.File, b []byte) (int, error) { n, _ := f.Write(b[:len(b)/2]); return n, failing },
		sync:     (*os.File).Sync,
		truncate: func(*os.File, int64) error { return stuck },
	}
	if _, err := l.Write([]*observev1.Observation{observation(spanB, "write_file", "2026-10-04T10:05:00Z")}, report("2026-10-04T10:06:00Z")); !errors.Is(err, ErrWrite) || !errors.Is(err, failing) || !errors.Is(err, stuck) {
		t.Errorf("Write = %v, want ErrWrite carrying both causes", err)
	}
	l.ops = osOps
	for range 2 {
		if _, err := l.Write(nil, report("2026-10-04T10:07:00Z")); !errors.Is(err, ErrFailed) {
			t.Errorf("a later Write = %v, want ErrFailed", err)
		}
	}
	mustDo(t, l.Close())
	l = openLog(t, dir)
	if got := contents(t, path); !bytes.Equal(got, before) {
		t.Errorf("after Open the file holds %d bytes, want the %d before the failed write", len(got), len(before))
	}
	if got := write(t, l, report("2026-10-04T10:08:00Z"), observation(spanB, "write_file", "2026-10-04T10:05:00Z")); got.Observations != 1 {
		t.Errorf("the failed write's observation was indexed: %+v", got)
	}
}

// TestAShortWriteWithNoErrorIsCutBack: a write that reports fewer bytes and
// no error failed all the same.
func TestAShortWriteWithNoErrorIsCutBack(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	path := filepath.Join(dir, FileName)
	write(t, l, report("2026-10-04T10:05:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
	before := contents(t, path)
	l.ops.write = func(f *os.File, b []byte) (int, error) { return f.Write(b[:len(b)-1]) }
	if _, err := l.Write(nil, report("2026-10-04T10:06:00Z")); !errors.Is(err, ErrWrite) || !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("Write = %v, want ErrWrite carrying io.ErrShortWrite", err)
	}
	if got := contents(t, path); !bytes.Equal(got, before) {
		t.Errorf("the short write left %q", got[len(before):])
	}
	l.ops = osOps
	if got := write(t, l, report("2026-10-04T10:07:00Z"), observation(spanB, "write_file", "2026-10-04T10:05:00Z")); got.Observations != 1 {
		t.Errorf("after the short write, Write = %+v", got)
	}
}
