//go:build unix

package observelog

import (
	"bytes"
	"errors"
	"io/fs"
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

// TestTheExportJudgesTheFileAsTheReaderDoes: a link, even to the log itself,
// a log the group or others may reach, one of another account and one with a
// second name are refused before a byte is written; the log itself exports.
func TestTheExportJudgesTheFileAsTheReaderDoes(t *testing.T) {
	export := func(path string) (string, error) {
		var out bytes.Buffer
		_, err := ExportFile(path, Query{Limit: DefaultExportLimit}, &out)
		return out.String(), err
	}
	fresh := func() string {
		dir := logDir(t)
		write(t, openLog(t, dir), report("2026-10-04T10:05:00Z"))
		return filepath.Join(dir, FileName)
	}
	path := fresh()
	if out, err := export(path); err != nil || !strings.Contains(out, `"type":"trailer"`) {
		t.Fatalf("the log itself: %v, %q; want an export", err, out)
	}
	link := filepath.Join(filepath.Dir(path), "link")
	mustDo(t, os.Symlink(FileName, link))
	if out, err := export(link); !errors.Is(err, ErrNotRegular) || out != "" {
		t.Errorf("a link to the log: %v, wrote %q; want ErrNotRegular and nothing", err, out)
	}

	for _, mode := range []os.FileMode{0o640, 0o620, 0o610, 0o604, 0o602, 0o601} {
		path := fresh()
		mustDo(t, os.Chmod(path, mode))
		if out, err := export(path); !errors.Is(err, ErrFileMode) || out != "" {
			t.Errorf("mode %04o: %v, wrote %q; want ErrFileMode and nothing", mode, err, out)
		}
	}

	path = fresh()
	mustDo(t, os.Link(path, filepath.Join(filepath.Dir(path), "second")))
	if out, err := export(path); !errors.Is(err, ErrLinks) || out != "" {
		t.Errorf("a second name: %v, wrote %q; want ErrLinks and nothing", err, out)
	}

	path = fresh()
	wantOwner(t, func(fs.FileInfo) int { return os.Geteuid() + 1 })
	if out, err := export(path); !errors.Is(err, ErrOwner) || out != "" {
		t.Errorf("another account's: %v, wrote %q; want ErrOwner and nothing", err, out)
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
