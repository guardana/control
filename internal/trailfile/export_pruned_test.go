package trailfile

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/coverage"
)

// exportOf is the export of the log in dir from its start, framed and as
// coverage reads it.
func exportOf(t *testing.T, dir string) (record, *coverage.Export) {
	t.Helper()
	var out bytes.Buffer
	if _, err := ExportLog(dir, Query{Limit: 1000}, &out); err != nil {
		t.Fatal(err)
	}
	header, _, _ := frame(t, out.String())
	x, err := coverage.ReadExport(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("coverage refuses the export: %v", err)
	}
	return header, x
}

// TestAnExportAfterAPruneCountsWhatIsGone: the export of the first segment
// left after a prune, read to the log's end, names in its header the segments
// pruned before it, and coverage does not take it as whole; the same log before the prune, exported
// to its end, names none and is whole.
func TestAnExportAfterAPruneCountsWhatIsGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "log")
	l := openTestLog(t, logConfig(dir))
	appendTo(t, l, event(1), event(2))
	appendTo(t, l, event(3))
	if err := l.rl.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(1); err != nil {
		t.Fatal(err)
	}
	h, x := exportOf(t, dir)
	if h.Version != "1.1" || h.Pruned == nil || *h.Pruned != 1 || filepath.Base(h.File) != "segment-00000002.jsonl" {
		t.Fatalf("after pruning one segment the header is %+v", h)
	}
	if x.Whole || !strings.Contains(x.NotWhole, "pruned") {
		t.Errorf("coverage takes the export after a prune as whole %v (%q)", x.Whole, x.NotWhole)
	}

	whole := filepath.Join(t.TempDir(), "log")
	w := openTestLog(t, logConfig(whole))
	appendTo(t, w, event(1), event(2))
	h, x = exportOf(t, whole)
	if h.Pruned == nil || *h.Pruned != 0 || !x.Whole {
		t.Errorf("a log with nothing pruned: pruned_before %v, whole %v (%q); want 0 and whole", h.Pruned, x.Whole, x.NotWhole)
	}
}

// TestAFilesExportCountsNothingPruned: a single file's export names 0.
func TestAFilesExportCountsNothingPruned(t *testing.T) {
	e := runExport(t, file(lineE1, lineE2), false, query())
	if e.header.Version != "1.1" || e.header.Pruned == nil || *e.header.Pruned != 0 {
		t.Errorf("a file's header is %+v, want version 1.1 and pruned_before 0", e.header)
	}
}
