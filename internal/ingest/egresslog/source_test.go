package egresslog_test

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/guardana/control/internal/ingest/egresslog"
)

// failingReader fails every read with err.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

// TestASourceFailureWrappingEOFFailsTheImport: complete lines followed by a
// read failing with an error that wraps io.EOF fail the import, since only
// io.EOF itself ends the input; the same lines ended by io.EOF import whole.
func TestASourceFailureWrappingEOFFailsTheImport(t *testing.T) {
	in := readTestdata(t, "envoy.jsonl")
	failed := io.MultiReader(bytes.NewReader(in), failingReader{fmt.Errorf("source failed: %w", io.EOF)})
	if _, err := egresslog.Import(failed, forwardProxy(t), descSHA, received); err == nil || !strings.Contains(err.Error(), "source failed") {
		t.Errorf("an import whose source failed: %v; want the failure", err)
	}
	b, err := egresslog.Import(io.MultiReader(bytes.NewReader(in)), forwardProxy(t), descSHA, received)
	if err != nil {
		t.Fatalf("an import ended by io.EOF: %v", err)
	}
	if got := b.Report.GetCounts().GetRead(); got != 4 {
		t.Errorf("an import ended by io.EOF read %d lines, want 4", got)
	}
}
