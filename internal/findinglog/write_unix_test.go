//go:build unix

package findinglog

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

func TestWriteAppendsTheFindingsThenTheReport(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	rep := report()
	rep.FindingsWritten = 9
	got, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idA, confirmed, "repeated_denial"), finding(idB, suspected, "outside")}, rep)
	if err != nil || got.Written != 2 || got.Duplicates != 0 || len(got.Conflicts) != 0 {
		t.Fatalf("Write = %+v, %v; want 2 written", got, err)
	}
	sameRecords(t, "the log", readBack(t, dir),
		findingRecord(finding(idA, confirmed, "repeated_denial")), findingRecord(finding(idB, suspected, "outside")), writtenReport(2))
	if rep.FindingsWritten != 9 {
		t.Errorf("Write changed the caller's report to %d findings written", rep.FindingsWritten)
	}
}

// TestADuplicateAndAConflictAreNotWrittenAndTheWriteCommits: the second
// write comes after a reopen, so only the index the open rebuilt finds the
// key; a verdict that changes is a new key.
func TestADuplicateAndAConflictAreNotWrittenAndTheWriteCommits(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, finding(idA, confirmed, "repeated_denial"))
	mustDo(t, l.Close())
	l = openLog(t, dir)
	other := finding(idA, confirmed, "repeated_denial")
	other.Finding.RuleVersion = "2"
	got := write(t, l, finding(idA, confirmed, "repeated_denial"), other, finding(idA, suspected, "repeated_denial"))
	if got.Written != 1 || got.Duplicates != 1 || !slices.Equal(got.Conflicts, []string{idA}) {
		t.Errorf("Write = %+v, want 1 written, 1 duplicate and %s a conflict", got, idA)
	}
	sameRecords(t, "the log", readBack(t, dir),
		findingRecord(finding(idA, confirmed, "repeated_denial")), writtenReport(1),
		findingRecord(finding(idA, suspected, "repeated_denial")), writtenReport(1))
}

// TestOneWriteFindsTheKeysItAppendsItself: the index the open built holds
// nothing, so only what the write adds finds the second and third.
func TestOneWriteFindsTheKeysItAppendsItself(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	other := finding(idA, confirmed, "repeated_denial")
	other.Escalation = findingv1alpha1.Escalation_ESCALATION_INFORM
	got := write(t, l, finding(idA, confirmed, "repeated_denial"), finding(idA, confirmed, "repeated_denial"), other)
	if got.Written != 1 || got.Duplicates != 1 || !slices.Equal(got.Conflicts, []string{idA}) {
		t.Errorf("Write = %+v, want one of each", got)
	}
	got = write(t, l, finding(idA, confirmed, "repeated_denial"))
	if got.Written != 0 || got.Duplicates != 1 {
		t.Errorf("the same finding in the next write = %+v, want a duplicate", got)
	}
	sameRecords(t, "the log", readBack(t, dir), findingRecord(finding(idA, confirmed, "repeated_denial")), writtenReport(1), writtenReport(0))
}

// refusedFindings are records the writer refuses, each a valid finding with
// one change, so a check that is removed lets its record through.
func refusedFindings(t testing.TB) map[string]*findingv1alpha1.FindingRecord {
	change := func(f func(*findingv1alpha1.FindingRecord)) *findingv1alpha1.FindingRecord {
		r := finding(idB, suspected, "outside")
		f(r)
		return r
	}
	return map[string]*findingv1alpha1.FindingRecord{
		"nil":                     nil,
		"version 0.2":             change(func(r *findingv1alpha1.FindingRecord) { r.SchemaVersion = "0.2" }),
		"no version":              change(func(r *findingv1alpha1.FindingRecord) { r.SchemaVersion = "" }),
		"no tenant":               change(func(r *findingv1alpha1.FindingRecord) { r.TenantId = "" }),
		"no project":              change(func(r *findingv1alpha1.FindingRecord) { r.ProjectId = "" }),
		"no finding":              change(func(r *findingv1alpha1.FindingRecord) { r.Finding = nil }),
		"an id of 31 digits":      change(func(r *findingv1alpha1.FindingRecord) { r.Finding.FindingId = idB[:35] }),
		"an id of 33 digits":      change(func(r *findingv1alpha1.FindingRecord) { r.Finding.FindingId = idB + "0" }),
		"an upper-case id":        change(func(r *findingv1alpha1.FindingRecord) { r.Finding.FindingId = "fnd-FEDCBA9876543210fedcba9876543210" }),
		"an id of another prefix": change(func(r *findingv1alpha1.FindingRecord) { r.Finding.FindingId = "obs-fedcba9876543210fedcba9876543210" }),
		"a heuristic source": change(func(r *findingv1alpha1.FindingRecord) {
			r.Finding.Source = controlv1.FindingSource_FINDING_SOURCE_HEURISTIC
		}),
		"a model source": change(func(r *findingv1alpha1.FindingRecord) {
			r.Finding.Source = controlv1.FindingSource_FINDING_SOURCE_MODEL
		}),
		"no source":              change(func(r *findingv1alpha1.FindingRecord) { r.Finding.Source = 0 }),
		"no verdict":             change(func(r *findingv1alpha1.FindingRecord) { r.Finding.Verdict = 0 }),
		"an unknown verdict":     change(func(r *findingv1alpha1.FindingRecord) { r.Finding.Verdict = 9 }),
		"no severity":            change(func(r *findingv1alpha1.FindingRecord) { r.Finding.Severity = 0 }),
		"an unknown severity":    change(func(r *findingv1alpha1.FindingRecord) { r.Finding.Severity = 6 }),
		"no escalation":          change(func(r *findingv1alpha1.FindingRecord) { r.Escalation = 0 }),
		"an unknown escalation":  change(func(r *findingv1alpha1.FindingRecord) { r.Escalation = 3 }),
		"evidence refs":          change(func(r *findingv1alpha1.FindingRecord) { r.Finding.EvidenceRefs = []string{"evt-1"} }),
		"a request id":           change(func(r *findingv1alpha1.FindingRecord) { r.Finding.RequestId = "req-1" }),
		"a recommended action":   change(func(r *findingv1alpha1.FindingRecord) { r.Finding.RecommendedAction = "stop" }),
		"framework mappings":     change(func(r *findingv1alpha1.FindingRecord) { r.Finding.FrameworkMappings = []string{"x"} }),
		"a line over the bound":  padded(t, MaxLineBytes+1).GetFindingRecord(),
		"a field it cannot name": change(func(r *findingv1alpha1.FindingRecord) { r.Refs[0].ProtoReflect().SetUnknown([]byte{0xf8, 0x01, 0x01}) }),
	}
}

func refusedReports() map[string]*findingv1alpha1.SuperviseReport {
	change := func(f func(*findingv1alpha1.SuperviseReport)) *findingv1alpha1.SuperviseReport {
		r := report()
		f(r)
		return r
	}
	return map[string]*findingv1alpha1.SuperviseReport{
		"nil":                    nil,
		"version 0.2":            change(func(r *findingv1alpha1.SuperviseReport) { r.SchemaVersion = "0.2" }),
		"no tenant":              change(func(r *findingv1alpha1.SuperviseReport) { r.TenantId = "" }),
		"no project":             change(func(r *findingv1alpha1.SuperviseReport) { r.ProjectId = "" }),
		"a run id of 31 digits":  change(func(r *findingv1alpha1.SuperviseReport) { r.RunId = runID[:35] }),
		"a run id of upper case": change(func(r *findingv1alpha1.SuperviseReport) { r.RunId = "run-00112233445566778899AABBCCDDEEFF" }),
		"a run id of no prefix":  change(func(r *findingv1alpha1.SuperviseReport) { r.RunId = runID[4:] + "0000" }),
		"a field it cannot name": change(func(r *findingv1alpha1.SuperviseReport) { r.Read.ProtoReflect().SetUnknown([]byte{0xf8, 0x01, 0x01}) }),
		"a rule it cannot name": change(func(r *findingv1alpha1.SuperviseReport) {
			r.Rules[0].ProtoReflect().SetUnknown([]byte{0xf8, 0x01, 0x01})
		}),
		"no version":              change(func(r *findingv1alpha1.SuperviseReport) { r.SchemaVersion = "" }),
		"version 0.1 with spaces": change(func(r *findingv1alpha1.SuperviseReport) { r.SchemaVersion = " 0.1" }),
	}
}

// TestWriteRefusesARecordItWillNotWriteAndWritesNothing: the refused finding
// follows one the log takes, and the log takes that one after the refusal,
// so the refused write neither wrote nor indexed it.
func TestWriteRefusesARecordItWillNotWriteAndWritesNothing(t *testing.T) {
	cases := map[string]func(*Log) error{}
	for name, bad := range refusedFindings(t) {
		cases["finding: "+name] = func(l *Log) error {
			_, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idA, confirmed, "repeated_denial"), bad}, report())
			return err
		}
	}
	for name, bad := range refusedReports() {
		cases["report: "+name] = func(l *Log) error {
			_, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idA, confirmed, "repeated_denial")}, bad)
			return err
		}
	}
	for name, refused := range cases {
		dir := logDir(t)
		l := openLog(t, dir)
		opened := contents(t, filepath.Join(dir, FileName))
		if err := refused(l); !errors.Is(err, ErrRecord) {
			t.Errorf("%s: Write = %v, want ErrRecord", name, err)
		}
		if b := contents(t, filepath.Join(dir, FileName)); !bytes.Equal(b, opened) {
			t.Errorf("%s: the refused Write left %q", name, b)
		}
		if got := write(t, l, finding(idA, confirmed, "repeated_denial"), finding(idB, suspected, "outside")); got.Written != 2 {
			t.Errorf("%s: the refused Write indexed a finding: %+v", name, got)
		}
	}
}

// TestWriteTakesALineOfTheBound: a line of the bound is written whole; the
// refusal of one byte more is in the table above.
func TestWriteTakesALineOfTheBound(t *testing.T) {
	dir := logDir(t)
	at := padded(t, MaxLineBytes).GetFindingRecord()
	if got := write(t, openLog(t, dir), at); got.Written != 1 {
		t.Fatalf("Write = %+v, want the finding written", got)
	}
	sameRecords(t, "the log", readBack(t, dir), padded(t, MaxLineBytes), writtenReport(1))
}

// TestAWriteThatWouldPassTheFileBoundIsRefused: the bound is lowered to the
// length one write leaves, measured on another log.
func TestAWriteThatWouldPassTheFileBoundIsRefused(t *testing.T) {
	measured := logDir(t)
	write(t, openLog(t, measured), finding(idA, confirmed, "repeated_denial"))
	size := int64(len(contents(t, filepath.Join(measured, FileName))))
	for _, c := range []struct {
		limit int64
		want  error
	}{{size, nil}, {size - 1, ErrTooLarge}} {
		dir := logDir(t)
		l := openLog(t, dir)
		l.limit = c.limit
		_, err := l.Write([]*findingv1alpha1.FindingRecord{finding(idA, confirmed, "repeated_denial")}, report())
		if !errors.Is(err, c.want) || (err == nil) != (c.want == nil) {
			t.Errorf("limit %d: Write = %v, want %v", c.limit, err, c.want)
		}
		if got := int64(len(contents(t, filepath.Join(dir, FileName)))); (c.want == nil) != (got == size) || (c.want != nil && got != int64(len(header02))) {
			t.Errorf("limit %d: the log holds %d bytes", c.limit, got)
		}
	}
}

func TestAClosedLogRefusesAWrite(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	opened := contents(t, filepath.Join(dir, FileName))
	mustDo(t, l.Close())
	mustDo(t, l.Close())
	if _, err := l.Write(nil, report()); !errors.Is(err, ErrClosed) {
		t.Errorf("Write = %v, want ErrClosed", err)
	}
	if _, err := (&Log{}).Write(nil, report()); !errors.Is(err, ErrClosed) {
		t.Errorf("a zero Log: Write = %v, want ErrClosed", err)
	}
	if b := contents(t, filepath.Join(dir, FileName)); !bytes.Equal(b, opened) {
		t.Errorf("the refused writes left %q", b)
	}
}

// TestLineIsTheBytesTheWriterWrites: the file is Line of each record the
// write appended, each followed by its newline, and Line refuses what the
// writer refuses.
func TestLineIsTheBytesTheWriterWrites(t *testing.T) {
	dir := logDir(t)
	a, b := finding(idA, confirmed, "repeated\u0085denial"), finding(idB, suspected, "outside")
	l := openLog(t, dir)
	want := contents(t, filepath.Join(dir, FileName))
	write(t, l, a, b)
	for _, r := range []*findingv1alpha1.Record{findingRecord(a), findingRecord(b), writtenReport(2)} {
		l, err := Line(r)
		mustDo(t, err)
		want = append(append(want, l...), '\n')
	}
	if got := contents(t, filepath.Join(dir, FileName)); !bytes.Equal(got, want) {
		t.Errorf("the log is\n%q\nwant\n%q", got, want)
	}
	if l, err := Line(&findingv1alpha1.Record{}); err == nil || l != nil {
		t.Errorf("Line of no record = %q, %v; want a refusal", l, err)
	}
}
