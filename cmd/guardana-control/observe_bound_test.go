package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheImportInputBoundIsPinned: the reference states the bound as
// 256 MiB.
func TestTheImportInputBoundIsPinned(t *testing.T) {
	if maxInputBytes != 268435456 || inputBound != 268435456 {
		t.Fatalf("maxInputBytes = %d, inputBound = %d; want 268435456 for both", maxInputBytes, inputBound)
	}
}

// TestObserveImportRefusesAnInputOverTheBoundWhole: an input one byte over
// the bound exits 2 and writes nothing, and the same input at the bound is
// imported.
func TestObserveImportRefusesAnInputOverTheBoundWhole(t *testing.T) {
	saved := inputBound
	t.Cleanup(func() { inputBound = saved })
	for _, c := range []struct {
		name  string
		slack int64
		code  int
	}{
		{"one byte over the bound", -1, exitUsage},
		{"at the bound", 0, exitOK},
	} {
		tr := newObserveTree(t)
		info, err := os.Stat(tr.input)
		if err != nil {
			t.Fatal(err)
		}
		inputBound = info.Size() + c.slack
		code, out, stderr := invoke(t, tr.importArgs()...)
		if code != c.code {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d", c.name, code, out, stderr, c.code)
		}
		_, statErr := os.Stat(filepath.Join(tr.log, "observations.jsonl"))
		if c.code == exitUsage && (out != "" || !strings.Contains(stderr, "over the size bound") || statErr == nil) {
			t.Errorf("%s: stdout %q, stderr %q, log written %v; want nothing written and the size refusal", c.name, out, stderr, statErr == nil)
		}
	}
}
