package otelgenai_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/ingest/otelgenai"
	"google.golang.org/protobuf/proto"
)

func TestMixedServiceFile(t *testing.T) {
	b := importFile(t, "mixed_service.jsonl")
	wantCounts(t, b, &observev1.ImportCounts{
		Read: 10, Observed: 4, OtherResource: 3, ContentAttributesDropped: 4,
		Skipped: map[string]uint64{"operation:retrieval": 1, "no_operation": 1, "unmapped_operation": 1},
	})
	if len(b.Observations) != 4 {
		t.Fatalf("%d observations, want 4", len(b.Observations))
	}
	rt := ts(t, "2026-10-04T10:05:00Z")
	want := []*observev1.Observation{{
		SchemaVersion: "0.1", ObservationId: obsID(traceA, 1), TenantId: "tenant-a", ProjectId: "project-a",
		Source: source(), EventTime: ts(t, "2026-10-04T10:00:00.25Z"), ReceivedTime: rt,
		Stage:                    observev1.Stage_STAGE_COMPLETED,
		Subject:                  &observev1.Subject{Kind: observev1.SubjectKind_SUBJECT_KIND_TOOL, Name: "read_file", Operation: "execute_tool", Provider: "local", ServerAddress: "files.example"},
		Outcome:                  &observev1.Outcome{Status: observev1.Status_STATUS_OK},
		Correlation:              &observev1.Correlation{Basis: observev1.Basis_BASIS_CLAIMED, RunId: runA, TraceId: traceA, SpanId: "00f067aa0ba90001", ParentSpanId: "b7ad6b7169203331"},
		ContentAttributesDropped: 1,
	}, {
		SchemaVersion: "0.1", ObservationId: obsID(traceA, 2), TenantId: "tenant-a", ProjectId: "project-a",
		Source: source(), EventTime: ts(t, "2026-10-04T10:00:01.5Z"), ReceivedTime: rt,
		Stage:                    observev1.Stage_STAGE_COMPLETED,
		Subject:                  &observev1.Subject{Kind: observev1.SubjectKind_SUBJECT_KIND_MODEL, Name: "model-a", Operation: "chat", Provider: "provider-a", ServerAddress: "api.example"},
		Outcome:                  &observev1.Outcome{Status: observev1.Status_STATUS_UNSET},
		Correlation:              &observev1.Correlation{Basis: observev1.Basis_BASIS_NONE, TraceId: traceA, SpanId: "00f067aa0ba90002"},
		ContentAttributesDropped: 2,
	}, {
		SchemaVersion: "0.1", ObservationId: obsID(traceA, 3), TenantId: "tenant-a", ProjectId: "project-a",
		Source: source(), EventTime: ts(t, "2026-10-04T10:00:02Z"), ReceivedTime: rt,
		Stage:                    observev1.Stage_STAGE_FAILED,
		Subject:                  &observev1.Subject{Kind: observev1.SubjectKind_SUBJECT_KIND_AGENT, Name: "support", Operation: "invoke_agent"},
		Outcome:                  &observev1.Outcome{Status: observev1.Status_STATUS_ERROR, ErrorType: "timeout"},
		Correlation:              &observev1.Correlation{Basis: observev1.Basis_BASIS_NONE, TraceId: traceA, SpanId: "00f067aa0ba90003"},
		ContentAttributesDropped: 1,
	}, {
		SchemaVersion: "0.1", ObservationId: obsID(traceB, 10), TenantId: "tenant-a", ProjectId: "project-a",
		Source: source(), EventTime: ts(t, "2026-10-04T10:00:03Z"), ReceivedTime: rt,
		Stage:       observev1.Stage_STAGE_COMPLETED,
		Subject:     &observev1.Subject{Kind: observev1.SubjectKind_SUBJECT_KIND_AGENT, Name: "planner", Operation: "create_agent"},
		Outcome:     &observev1.Outcome{Status: observev1.Status_STATUS_UNSET},
		Correlation: &observev1.Correlation{Basis: observev1.Basis_BASIS_NONE, TraceId: traceB, SpanId: "00f067aa0ba9000a"},
	}}
	for i := range want {
		equal(t, b.Observations[i], want[i])
	}
	equal(t, b.Report, &observev1.ImportReport{
		SchemaVersion: "0.1", TenantId: "tenant-a", ProjectId: "project-a", Source: source(), ReceivedTime: rt,
		InputFirstLineSha256: firstLineSHA256(readTestdata(t, "mixed_service.jsonl")), InputBytes: 4958, Counts: b.Report.GetCounts(),
		EarliestEventTime: ts(t, "2026-10-04T10:00:00.25Z"), LatestEventTime: ts(t, "2026-10-04T10:00:03Z"),
	})
}

func TestReimportYieldsIdenticalRecords(t *testing.T) {
	first, second := importFile(t, "mixed_service.jsonl"), importFile(t, "mixed_service.jsonl")
	if len(first.Observations) != len(second.Observations) {
		t.Fatalf("%d and %d observations", len(first.Observations), len(second.Observations))
	}
	for i := range first.Observations {
		equal(t, second.Observations[i], first.Observations[i])
	}
	equal(t, second.Report, first.Report)
}

func TestSampledSourceImportsTheSame(t *testing.T) {
	complete := importFile(t, "mixed_service.jsonl")
	partial := descriptor(t)
	partial.Sampling = observev1.Sampling_SAMPLING_PARTIAL
	got := importBytes(t, readTestdata(t, "mixed_service.jsonl"), partial)
	if len(got.Observations) != len(complete.Observations) {
		t.Fatalf("%d and %d observations", len(got.Observations), len(complete.Observations))
	}
	for i := range got.Observations {
		equal(t, got.Observations[i], complete.Observations[i])
	}
	equal(t, got.Report, complete.Report)
}

func TestDescriptorNotServed(t *testing.T) {
	cases := map[string]func(d *observev1.SourceDescriptor){
		"kind":               func(d *observev1.SourceDescriptor) { d.Kind = "otel-genai-logs" },
		"convention name":    func(d *observev1.SourceDescriptor) { d.Convention.Name = "opentelemetry.http" },
		"convention version": func(d *observev1.SourceDescriptor) { d.Convention.Version = "1.40.0" },
		"otlp version":       func(d *observev1.SourceDescriptor) { d.OtlpVersion = "1.10.0" },
		"schema version":     func(d *observev1.SourceDescriptor) { d.SchemaVersion = "0.2" },
		"independent trust":  func(d *observev1.SourceDescriptor) { d.Trust = observev1.Trust_TRUST_INDEPENDENT },
		"no service":         func(d *observev1.SourceDescriptor) { d.Select = nil },
	}
	in := readTestdata(t, "mixed_service.jsonl")
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := descriptor(t)
			mutate(d)
			b, err := otelgenai.Import(bytes.NewReader(in), d, descSHA, received)
			if !errors.Is(err, otelgenai.ErrDescriptor) || b.Report != nil || b.Observations != nil {
				t.Fatalf("Import = %v, %v; want ErrDescriptor and nothing", b, err)
			}
		})
	}
	for _, sha := range []string{"", descSHA[:63], "9F86" + descSHA[4:], descSHA + "0"} {
		if _, err := otelgenai.Import(bytes.NewReader(in), descriptor(t), sha, received); !errors.Is(err, otelgenai.ErrDescriptor) {
			t.Errorf("digest %q: %v, want ErrDescriptor", sha, err)
		}
	}
	if _, err := otelgenai.Import(bytes.NewReader(in), nil, descSHA, received); !errors.Is(err, otelgenai.ErrDescriptor) {
		t.Errorf("nil descriptor: %v, want ErrDescriptor", err)
	}
}

func TestReceivedTimeRefused(t *testing.T) {
	in := readTestdata(t, "mixed_service.jsonl")
	for _, rt := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := otelgenai.Import(bytes.NewReader(in), descriptor(t), descSHA, rt); !errors.Is(err, otelgenai.ErrReceived) {
			t.Errorf("received %v: %v, want ErrReceived", rt, err)
		}
	}
	if _, err := otelgenai.Import(bytes.NewReader(in), descriptor(t), descSHA, time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Errorf("received in year 9999: %v", err)
	}
}

func TestReaderFailureIsAnError(t *testing.T) {
	failure := errors.New("disk gone")
	in := io.MultiReader(bytes.NewReader(readTestdata(t, "mixed_service.jsonl")), iotest.ErrReader(failure))
	b, err := otelgenai.Import(in, descriptor(t), descSHA, received)
	if !errors.Is(err, failure) || b.Report != nil || b.Observations != nil {
		t.Fatalf("Import = %v, %v; want the reader's error and nothing", b, err)
	}
	if _, err := otelgenai.Import(nil, descriptor(t), descSHA, received); err == nil {
		t.Fatal("a nil reader imported")
	}
}

func TestRecordsShareNoMutableState(t *testing.T) {
	b := importFile(t, "mixed_service.jsonl")
	b.Observations[0].ReceivedTime.Seconds = 1
	b.Observations[0].Source.SourceId = "changed"
	if b.Observations[1].GetReceivedTime().GetSeconds() == 1 || b.Report.GetReceivedTime().GetSeconds() == 1 ||
		b.Observations[1].GetSource().GetSourceId() == "changed" || b.Report.GetSource().GetSourceId() == "changed" {
		t.Fatal("changing one record changed another")
	}
	if !proto.Equal(b.Observations[1].GetSource(), source()) {
		t.Fatal("source changed")
	}
}
