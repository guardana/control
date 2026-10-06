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

// TestAWriteThatWouldPassTheFileBoundIsRefused: the bound is lowered to the
// length one write leaves, measured on another log; a write one byte past it
// is refused and leaves the file empty, so Open never meets a log it refuses.
func TestAWriteThatWouldPassTheFileBoundIsRefused(t *testing.T) {
	if l := openLog(t, logDir(t)); l.limit != MaxLogBytes {
		t.Fatalf("an open log's bound is %d, want MaxLogBytes", l.limit)
	}
	o, rep := observation(spanA, "read_file", "2026-10-04T10:05:00Z"), report("2026-10-04T10:05:00Z")
	measured := logDir(t)
	write(t, openLog(t, measured), rep, o)
	size := int64(len(contents(t, filepath.Join(measured, FileName))))
	for _, c := range []struct {
		limit int64
		want  error
	}{{size, nil}, {size - 1, ErrTooLarge}} {
		dir := logDir(t)
		l := openLog(t, dir)
		l.limit = c.limit
		_, err := l.Write([]*observev1.Observation{o}, rep)
		if !errors.Is(err, c.want) || (err == nil) != (c.want == nil) {
			t.Errorf("limit %d: Write = %v, want %v", c.limit, err, c.want)
		}
		if got := int64(len(contents(t, filepath.Join(dir, FileName)))); (c.want == nil) != (got == size) || (c.want != nil && got != 0) {
			t.Errorf("limit %d: the log holds %d bytes", c.limit, got)
		}
		if c.want != nil {
			if _, err := l.Write(nil, rep); err != nil {
				t.Errorf("limit %d: a refused write refused the next one: %v", c.limit, err)
			}
		}
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
