package otelgenai_test

import (
	"bytes"
	"strings"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/ingest/otelgenai"
)

func TestBytesAfterTheLastNewlineAreNotRead(t *testing.T) {
	line := oneSpan("", "") + "\n"
	b := importBytes(t, []byte(line+oneSpan("", "")), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{Read: 1, Observed: 1})
	if got, want := b.Report.GetInputBytes(), uint64(len(line)); got != want {
		t.Errorf("input bytes %d, want %d", got, want)
	}
	if got, want := b.Report.GetInputFirstLineSha256(), firstLineSHA256([]byte(line)); got != want {
		t.Errorf("first line digest %s, want %s", got, want)
	}

	none := importBytes(t, []byte(oneSpan("", "")), descriptor(t))
	wantCounts(t, none, &observev1.ImportCounts{})
	if none.Report.GetInputBytes() != 0 || none.Report.GetInputFirstLineSha256() != "" {
		t.Errorf("input without a newline: %d bytes, digest %q; want 0 and none",
			none.Report.GetInputBytes(), none.Report.GetInputFirstLineSha256())
	}
}

func TestBlankLineIsRefused(t *testing.T) {
	b := importBytes(t, []byte("\n"+oneSpan("", "")+"\n"), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{Read: 2, Observed: 1, Refused: 1})
	if got := b.Report.GetInputFirstLineSha256(); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("first line digest %s, want the empty line's", got)
	}
}

// padded is one valid line of exactly n bytes before its newline.
func padded(t *testing.T, n int) []byte {
	t.Helper()
	line := oneSpan("", "")
	if n < len(line) {
		t.Fatalf("cannot pad %d bytes to %d", len(line), n)
	}
	return []byte(line + strings.Repeat(" ", n-len(line)) + "\n")
}

func TestLineBound(t *testing.T) {
	at := padded(t, otelgenai.MaxLineBytes)
	wantCounts(t, importBytes(t, at, descriptor(t)), &observev1.ImportCounts{Read: 1, Observed: 1})

	over := padded(t, otelgenai.MaxLineBytes+1)
	next := []byte(oneSpan("", "") + "\n")
	b := importBytes(t, append(append([]byte{}, over...), next...), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{Read: 2, Observed: 1, Refused: 1})
	if got, want := b.Report.GetInputBytes(), uint64(len(over)+len(next)); got != want {
		t.Errorf("input bytes %d, want %d", got, want)
	}
	if got, want := b.Report.GetInputFirstLineSha256(), firstLineSHA256(over); got != want {
		t.Errorf("an over-long first line's digest %s, want %s", got, want)
	}
}

func TestDuplicateSpanIsPassedThrough(t *testing.T) {
	line := oneSpan("", "") + "\n"
	b := importBytes(t, bytes.Repeat([]byte(line), 2), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{Read: 2, Observed: 2})
	if b.Observations[0].GetObservationId() != b.Observations[1].GetObservationId() {
		t.Error("one span read twice yields two ids")
	}
}
