package observelog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanA   = "00f067aa0ba902b7"
	spanB   = "b7ad6b7169203331"
	// idA is the id of tenant-a, project-a, agent-runtime, traceID and spanA,
	// as internal/observe's fixture spells it.
	idA = "obs-000c5bc88e611c5191006f3b210e071b"
)

// logDir is a fresh directory only this account may enter. t.TempDir is
// created 0755 here, which Open refuses.
func logDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "log")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory, owner-only, as the log requires
		t.Fatal(err)
	}
	return dir
}

func ts(s string) *timestamppb.Timestamp {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return timestamppb.New(v)
}

// observation is the span's observation with the tool named name, received
// at received.
func observation(span, name, received string) *observev1.Observation {
	return &observev1.Observation{
		SchemaVersion: "0.1",
		ObservationId: observe.ObservationID("tenant-a", "project-a", "agent-runtime", traceID, span),
		TenantId:      "tenant-a",
		ProjectId:     "project-a",
		Source:        &observev1.SourceRef{SourceId: "agent-runtime", DescriptorSha256: "d1", Trust: observev1.Trust_TRUST_SELF_REPORTED},
		EventTime:     ts("2026-10-04T10:00:00Z"),
		ReceivedTime:  ts(received),
		Stage:         observev1.Stage_STAGE_COMPLETED,
		Subject:       &observev1.Subject{Kind: observev1.SubjectKind_SUBJECT_KIND_TOOL, Name: name, Operation: "execute_tool"},
		Outcome:       &observev1.Outcome{Status: observev1.Status_STATUS_OK},
		Correlation:   &observev1.Correlation{Basis: observev1.Basis_BASIS_NONE, TraceId: traceID, SpanId: span},
	}
}

func report(received string) *observev1.ImportReport {
	return &observev1.ImportReport{
		SchemaVersion: "0.1",
		TenantId:      "tenant-a",
		ProjectId:     "project-a",
		Source:        &observev1.SourceRef{SourceId: "agent-runtime", DescriptorSha256: "d1"},
		ReceivedTime:  ts(received),
		Counts:        &observev1.ImportCounts{Read: 2},
	}
}

func openLog(t *testing.T, dir string) *Log {
	t.Helper()
	l, err := Open(dir)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func write(t *testing.T, l *Log, rep *observev1.ImportReport, obs ...*observev1.Observation) Written {
	t.Helper()
	got, err := l.Write(obs, rep)
	if err != nil {
		t.Fatalf("Write = %v", err)
	}
	return got
}

func contents(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: the test's own file
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// records decodes every line of the log file at path.
func records(t *testing.T, path string) []*observev1.Record {
	t.Helper()
	var out []*observev1.Record
	for _, line := range bytes.SplitAfter(contents(t, path), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		r, err := observe.UnmarshalLine(line)
		if err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
