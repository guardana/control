//go:build unix

package trailfile

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestExportFileAsksTheLockWhoHoldsTheFile: a tail is a line in progress
// while a writer holds the file, and a gap once none does.
func TestExportFileAsksTheLockWhoHoldsTheFile(t *testing.T) {
	path := trailPath(t)
	body := file(lineE1) + partialTail
	w := openWriter(t, path)
	// Opening the writer cuts the tail, so it is written again under it.
	writeFile(t, path, body)
	tr, err := ExportFile(path, query(), &bytes.Buffer{})
	if err != nil || !tr.WriterHeld || tr.Gaps != 0 || tr.TailBytes != int64(len(partialTail)) {
		t.Errorf("held: ExportFile = %+v, %v; want the tail held and no gap", tr, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, body)
	var out bytes.Buffer
	tr, err = ExportFile(path, query(), &out)
	if err != nil || tr.WriterHeld || tr.Gaps != 1 || !bytes.Contains(out.Bytes(), []byte(`"file":"`+path+`"`)) {
		t.Errorf("not held: ExportFile = %+v, %v with\n%s; want the tail a gap and the path in the header", tr, err, out.String())
	}
}

// TestExportFileRefusesWhatIsNotARegularFile: a directory, a missing file
// and a named pipe are refused, the pipe without waiting on its other end.
func TestExportFileRefusesWhatIsNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := ExportFile(dir, query(), &bytes.Buffer{}); !errors.Is(err, ErrNotRegular) {
		t.Errorf("a directory: %v", err)
	}
	if _, err := ExportFile(filepath.Join(dir, "none"), query(), &bytes.Buffer{}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no file: %v", err)
	}
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ExportFile(pipe, query(), &bytes.Buffer{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Errorf("a pipe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exporting a named pipe waited for its other end")
	}
}
