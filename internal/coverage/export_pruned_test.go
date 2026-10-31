package coverage_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/coverage"
)

// prunedHeader is a 1.1 header of a segment export whose member pruned_before
// is spelled value.
func prunedHeader(value string) string {
	return strings.Replace(strings.Replace(header(plainQuery), `"version":"1.0"`, `"version":"1.1"`, 1),
		`,"query":`, `,"pruned_before":`+value+`,"query":`, 1)
}

// TestAnExportOfALogWithSegmentsPrunedBeforeItIsNotWhole: a 1.1 header
// counting segments pruned before the exported one makes the export not
// whole, saying so; 0, and a 1.0 header without the member, leave it whole;
// a count that is not one is refused.
func TestAnExportOfALogWithSegmentsPrunedBeforeItIsNotWhole(t *testing.T) {
	ev := proposal{trace: traceA, span: spanA}.line()
	end := trailer(1, 0, 0, true)
	for value, whole := range map[string]bool{"0": true, "1": false, "2": false} {
		x := mustExport(t, lines(prunedHeader(value), ev, end))
		if x.Whole != whole || (!whole && !strings.Contains(x.NotWhole, "pruned")) {
			t.Errorf("pruned_before %s: whole %v (%q), want %v", value, x.Whole, x.NotWhole, whole)
		}
	}
	if x := mustExport(t, lines(header(plainQuery), ev, end)); !x.Whole {
		t.Errorf("a 1.0 header without the member: whole %v (%q), want whole", x.Whole, x.NotWhole)
	}
	for _, value := range []string{"-1", `"1"`, "1.5", "null"} {
		if _, err := coverage.ReadExport(bytes.NewReader(lines(prunedHeader(value), ev, end))); !errors.Is(err, coverage.ErrExport) {
			t.Errorf("pruned_before %s: %v, want ErrExport", value, err)
		}
	}
}
