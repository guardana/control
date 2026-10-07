package findinglog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/proto"
)

const (
	idA   = "fnd-0123456789abcdef0123456789abcdef"
	idB   = "fnd-fedcba9876543210fedcba9876543210"
	runID = "run-00112233445566778899aabbccddeeff"
)

const (
	confirmed = controlv1.FindingVerdict_FINDING_VERDICT_CONFIRMED
	suspected = controlv1.FindingVerdict_FINDING_VERDICT_SUSPECTED
)

// logDir is a fresh directory only this account may enter. t.TempDir is
// created 0755 here, which Open refuses.
func logDir(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "log")
	mustDo(t, os.Mkdir(dir, 0o700))
	mustDo(t, os.Chmod(dir, 0o700)) //nolint:gosec // G302: a directory, owner-only, as the log requires
	return dir
}

func procedure() *findingv1alpha1.ProcedureRef {
	return &findingv1alpha1.ProcedureRef{ProcedureId: "refund", Version: "1", Digest: "aa"}
}

// finding is a record the writer takes: the finding id with verdict, under
// rule.
func finding(id string, verdict controlv1.FindingVerdict, rule string) *findingv1alpha1.FindingRecord {
	return &findingv1alpha1.FindingRecord{
		SchemaVersion: "0.1",
		TenantId:      "tenant-a",
		ProjectId:     "project-a",
		Procedure:     procedure(),
		Escalation:    findingv1alpha1.Escalation_ESCALATION_ALERT,
		Refs: []*findingv1alpha1.Reference{
			{Ref: &findingv1alpha1.Reference_Event{Event: &findingv1alpha1.EventRef{EventId: "evt-1", RequestId: "req-1"}}},
		},
		Finding: &controlv1.Finding{
			FindingId:   id,
			RuleId:      rule,
			RuleVersion: "1",
			Severity:    controlv1.FindingSeverity_FINDING_SEVERITY_HIGH,
			Verdict:     verdict,
			RunId:       runID,
			Source:      controlv1.FindingSource_FINDING_SOURCE_DETERMINISTIC,
		},
	}
}

func report() *findingv1alpha1.SuperviseReport {
	return &findingv1alpha1.SuperviseReport{
		SchemaVersion: "0.1",
		TenantId:      "tenant-a",
		ProjectId:     "project-a",
		RunId:         runID,
		Procedure:     procedure(),
		Read:          &findingv1alpha1.ReadCounts{EventsTaken: 4, EventsLeftOut: map[string]uint64{"another run": 2}},
		Rules:         []*findingv1alpha1.RuleResult{{RuleId: "repeated_denial", RuleVersion: "1", State: findingv1alpha1.RuleState_RULE_STATE_CHECKED}},
	}
}

func findingRecord(f *findingv1alpha1.FindingRecord) *findingv1alpha1.Record {
	return &findingv1alpha1.Record{Record: &findingv1alpha1.Record_FindingRecord{FindingRecord: f}}
}

func reportRecord(r *findingv1alpha1.SuperviseReport) *findingv1alpha1.Record {
	return &findingv1alpha1.Record{Record: &findingv1alpha1.Record_SuperviseReport{SuperviseReport: r}}
}

// writtenReport is report() as the log writes it after n findings appended.
func writtenReport(n uint64) *findingv1alpha1.Record {
	r := report()
	r.FindingsWritten = n
	return reportRecord(r)
}

// line is the writer's line for r.
func line(t testing.TB, r *findingv1alpha1.Record) string {
	t.Helper()
	b, err := marshalLine(r)
	mustDo(t, err)
	return string(b)
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

func write(t *testing.T, l *Log, findings ...*findingv1alpha1.FindingRecord) Result {
	t.Helper()
	got, err := l.Write(findings, report())
	if err != nil {
		t.Fatalf("Write = %v", err)
	}
	return got
}

func contents(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: the test's own file
	mustDo(t, err)
	return b
}

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	mustDo(t, os.WriteFile(path, []byte(body), 0o600))
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // G304: the test's own file
	mustDo(t, err)
	_, err = f.WriteString(s)
	mustDo(t, errors.Join(err, f.Close()))
}

func mustDo(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// readAll is what ReadFile returns for the log in dir.
func readAll(t *testing.T, dir string) []*findingv1alpha1.Record {
	t.Helper()
	got, err := ReadFile(filepath.Join(dir, FileName))
	mustDo(t, err)
	return got
}

// readBack is what ReadFile returns for the log in dir after its header,
// which a log this package created starts with.
func readBack(t *testing.T, dir string) []*findingv1alpha1.Record {
	t.Helper()
	got := readAll(t, dir)
	if len(got) == 0 || !prefixedHex(got[0].GetLogHeader().GetLogId(), "log-") {
		t.Fatalf("the log does not start with a header: %v", got)
	}
	return got[1:]
}

// sameRecords fails t unless got holds want's records, in order.
func sameRecords(t *testing.T, what string, got []*findingv1alpha1.Record, want ...*findingv1alpha1.Record) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d records, want %d: %v", what, len(got), len(want), got)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("%s: record %d = %v, want %v", what, i, got[i], want[i])
		}
	}
}
