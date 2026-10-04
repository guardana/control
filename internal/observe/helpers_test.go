package observe_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	fixtureDescriptorSHA256 = "7602644155b2785d471d51830e7dd6f4fc86c779802ce6b63bfc7de5fdb2d043"
	fixtureTraceID          = "4bf92f3577b34da6a3ce929d0e0e4736"
	fixtureSpanID           = "00f067aa0ba902b7"
	fixtureParentSpanID     = "b7ad6b7169203331"
	fixtureRunID            = "run-0123456789abcdef0123456789abcdef"
	fixtureObservationID    = "obs-000c5bc88e611c5191006f3b210e071b"
)

func readTestdata(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return b
}

// fixtureLines returns the lines of testdata/records.jsonl, each with its
// newline: the observation first, the import report second.
func fixtureLines(t testing.TB) [][]byte {
	t.Helper()
	lines := bytes.SplitAfter(readTestdata(t, "records.jsonl"), []byte("\n"))
	if len(lines) != 3 || len(lines[2]) != 0 {
		t.Fatalf("records.jsonl holds %d parts, want two lines", len(lines))
	}
	return lines[:2]
}

func ts(s string) *timestamppb.Timestamp {
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return timestamppb.New(v)
}

func fixtureSource() *observev1.SourceRef {
	return &observev1.SourceRef{
		SourceId:         "agent-runtime",
		DescriptorSha256: fixtureDescriptorSHA256,
		Trust:            observev1.Trust_TRUST_SELF_REPORTED,
		Convention:       &observev1.Convention{Name: "opentelemetry.gen_ai", Version: "1.41.0"},
	}
}

// fixtureObservation is the first line of records.jsonl, written out by hand.
func fixtureObservation() *observev1.Observation {
	return &observev1.Observation{
		SchemaVersion: "0.1",
		ObservationId: fixtureObservationID,
		TenantId:      "tenant-a",
		ProjectId:     "project-a",
		Source:        fixtureSource(),
		EventTime:     ts("2026-10-04T10:00:00.250Z"),
		ReceivedTime:  ts("2026-10-04T10:05:00Z"),
		Stage:         observev1.Stage_STAGE_COMPLETED,
		Subject: &observev1.Subject{
			Kind:          observev1.SubjectKind_SUBJECT_KIND_TOOL,
			Name:          "read_file",
			Operation:     "execute_tool",
			Provider:      "local",
			ServerAddress: "files.example",
		},
		Outcome: &observev1.Outcome{Status: observev1.Status_STATUS_OK},
		Correlation: &observev1.Correlation{
			Basis:        observev1.Basis_BASIS_CLAIMED,
			RunId:        fixtureRunID,
			TraceId:      fixtureTraceID,
			SpanId:       fixtureSpanID,
			ParentSpanId: fixtureParentSpanID,
		},
		ContentAttributesDropped: 2,
	}
}

// fixtureReport is the second line of records.jsonl, written out by hand.
func fixtureReport() *observev1.ImportReport {
	return &observev1.ImportReport{
		SchemaVersion:        "0.1",
		TenantId:             "tenant-a",
		ProjectId:            "project-a",
		Source:               fixtureSource(),
		ReceivedTime:         ts("2026-10-04T10:05:00Z"),
		InputFirstLineSha256: "1de24ae78ad00c30f40262369efef16bbc959768a98ab18e9e8360622da73305",
		InputBytes:           4096,
		Counts: &observev1.ImportCounts{
			Read:                     3,
			Observed:                 1,
			Skipped:                  map[string]uint64{"unmapped_operation": 1},
			OtherResource:            1,
			ContentAttributesDropped: 2,
		},
		EarliestEventTime: ts("2026-10-04T10:00:00.250Z"),
		LatestEventTime:   ts("2026-10-04T10:00:00.250Z"),
	}
}

func obsRecord(o *observev1.Observation) *observev1.Record {
	return &observev1.Record{Record: &observev1.Record_Observation{Observation: o}}
}

func reportRecord(r *observev1.ImportReport) *observev1.Record {
	return &observev1.Record{Record: &observev1.Record_ImportReport{ImportReport: r}}
}

// jsonEdit decodes doc, applies edit to the generic tree and encodes it again,
// so a case states only the member it changes.
func jsonEdit(t testing.TB, doc []byte, edit func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encoding the edited fixture: %v", err)
	}
	return out
}

func member(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		m = m[p].(map[string]any)
	}
	return m
}
