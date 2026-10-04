//go:build unix

package observelog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/guardana/control/internal/lineexport"
)

// TestTheExportReadsALogAWriterHolds: the bytes after the last newline are a
// line still being written while the log is held, and a gap once it is not.
func TestTheExportReadsALogAWriterHolds(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, report("2026-10-04T10:05:00Z"), observation(spanA, "read_file", "2026-10-04T10:05:00Z"))
	path := filepath.Join(dir, FileName)
	appendTo(t, path, `{"observation":`)
	var held bytes.Buffer
	tr, err := ExportFile(path, Query{Limit: DefaultExportLimit}, &held)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.WriterHeld || tr.TailBytes != 15 || tr.Counts[lineexport.Gap] != 0 || tr.Counts["observation"] != 1 || tr.Counts["import_report"] != 1 {
		t.Errorf("while held: trailer %+v, want the writer held, 15 tail bytes, no gap", tr)
	}
	mustDo(t, l.Close())
	var free bytes.Buffer
	if tr, err = ExportFile(path, Query{Limit: DefaultExportLimit}, &free); err != nil {
		t.Fatal(err)
	}
	if tr.WriterHeld || tr.Counts[lineexport.Gap] != 1 || !strings.Contains(free.String(), `"reason":"partial_tail"`) {
		t.Errorf("once released: trailer %+v, want a partial_tail gap", tr)
	}
}

func TestTheExportRefusesWhatIsNoRegularFile(t *testing.T) {
	dir := logDir(t)
	fifo := filepath.Join(dir, "fifo")
	mustDo(t, syscall.Mkfifo(fifo, 0o600))
	for _, path := range []string{dir, fifo} {
		var out bytes.Buffer
		if _, err := ExportFile(path, Query{Limit: 1}, &out); !errors.Is(err, ErrNotRegular) || out.Len() != 0 {
			t.Errorf("%s: ExportFile = %v, wrote %d bytes; want ErrNotRegular and nothing", path, err, out.Len())
		}
	}
	if _, err := ExportFile(filepath.Join(dir, "missing"), Query{Limit: 1}, &bytes.Buffer{}); err == nil {
		t.Error("ExportFile of a missing file passed")
	}
}

// TestAskingWhetherAWriterHoldsTheFileLetsItGo: the export's probe takes a
// shared lock, and one it kept would refuse every writer while the export
// runs.
func TestAskingWhetherAWriterHoldsTheFileLetsItGo(t *testing.T) {
	dir := logDir(t)
	mustDo(t, openLogAndClose(dir))
	f, err := os.Open(filepath.Join(dir, FileName)) //nolint:gosec // G304: the log this test made in its own directory
	mustDo(t, err)
	defer func() { _ = f.Close() }()
	if held, err := heldByWriter(f); held || err != nil {
		t.Fatalf("heldByWriter = %v, %v; want no writer", held, err)
	}
	openLog(t, dir)
}
