package coverage_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/coverage"
)

// TestReadExportReadsEveryGolden reads the export goldens the trail file's
// exporter is pinned to, so the decoder here reads what that writer writes.
func TestReadExportReadsEveryGolden(t *testing.T) {
	dir := os.DirFS("../../testdata/export")
	want := map[string]struct {
		whole  bool
		events int
	}{
		"empty.jsonl":   {true, 0},
		"records.jsonl": {false, 3},
		"resumed.jsonl": {false, 2},
	}
	names, err := fs.Glob(dir, "*.jsonl")
	if err != nil || len(names) != len(want) {
		t.Fatalf("goldens %v, %v; want the %d this test names", names, err, len(want))
	}
	for _, name := range names {
		b, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		x, err := coverage.ReadExport(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if x.Whole != want[name].whole || x.Events != want[name].events || x.File != strings.TrimSuffix(name, ".jsonl")+".trail" {
			t.Errorf("%s: whole %v, %d events, file %q; want %+v", name, x.Whole, x.Events, x.File, want[name])
		}
		if !x.Whole && x.NotWhole == "" {
			t.Errorf("%s: not whole and no reason", name)
		}
	}
}

func TestReadExportWhole(t *testing.T) {
	ev := proposal{trace: traceA, span: spanA}.line()
	x := mustExport(t, lines(header(plainQuery), ev, trailer(1, 0, 0, true)))
	if !x.Whole || x.NotWhole != "" || x.Events != 1 {
		t.Fatalf("whole %v (%q), %d events; want whole with 1 event", x.Whole, x.NotWhole, x.Events)
	}
	dup := `{"type":"duplicate","offset":9,"event_id":"e1","first_offset":0}`
	x = mustExport(t, lines(header(plainQuery), ev, dup, trailer(1, 0, 1, true)))
	if !x.Whole || x.Events != 1 {
		t.Fatalf("with a duplicate: whole %v (%q), %d events; want whole with 1 event", x.Whole, x.NotWhole, x.Events)
	}
	if _, err := coverage.ReadExport(bytes.NewReader(lines(header(plainQuery), ev, dup, trailer(1, 0, 0, true)))); !errors.Is(err, coverage.ErrExport) {
		t.Fatalf("a duplicate the trailer does not count: %v, want ErrExport", err)
	}
}

func TestReadExportNotWhole(t *testing.T) {
	ev := proposal{trace: traceA, span: spanA}.line()
	gap := `{"type":"gap","offset":140,"cursor":"c","reason":"malformed"}`
	cases := map[string][]byte{
		"no trailer":            lines(header(plainQuery), ev),
		"a cut trailer":         append(lines(header(plainQuery), ev), trailer(1, 0, 0, true)[:20]...),
		"end not reached":       lines(header(plainQuery), ev, trailer(1, 0, 0, false)),
		"a gap":                 lines(header(plainQuery), ev, gap, trailer(1, 1, 0, true)),
		"a partial tail gap":    lines(header(plainQuery), ev, `{"type":"gap","offset":9,"reason":"partial_tail"}`, swap(trailer(1, 1, 0, true), `"tail_bytes":0`, `"tail_bytes":5`)),
		"after a cursor":        lines(header(`{"after":"v1:x","limit":1000}`), ev, trailer(1, 0, 0, true)),
		"filtered by request":   lines(header(`{"limit":1000,"request":["r1"]}`), ev, trailer(1, 0, 0, true)),
		"filtered by run":       lines(header(`{"limit":1000,"run":["run1"]}`), ev, trailer(1, 0, 0, true)),
		"filtered by tenant":    lines(header(`{"limit":1000,"tenant":["t1"]}`), ev, trailer(1, 0, 0, true)),
		"filtered by project":   lines(header(`{"limit":1000,"project":["p1"]}`), ev, trailer(1, 0, 0, true)),
		"filtered by kind":      lines(header(`{"limit":1000,"kind":["EVENT_KIND_ACTION_PROPOSED"]}`), ev, trailer(1, 0, 0, true)),
		"no newline at the end": bytes.TrimSuffix(lines(header(plainQuery), ev, trailer(1, 0, 0, true)), []byte("\n")),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			x := mustExport(t, b)
			if x.Whole || x.NotWhole == "" {
				t.Fatalf("whole %v, reason %q; want not whole with a reason", x.Whole, x.NotWhole)
			}
		})
	}
}

func TestReadExportRefuses(t *testing.T) {
	ev := proposal{trace: traceA, span: spanA}.line()
	end := trailer(1, 0, 0, true)
	cases := map[string][]byte{
		"nothing":                 nil,
		"a blank line":            lines(header(plainQuery), "", ev, end),
		"no header":               lines(ev, end),
		"a header twice":          lines(header(plainQuery), header(plainQuery), ev, end),
		"another format":          lines(strings.Replace(header(plainQuery), "evidence-export", "observation-export", 1), ev, end),
		"another major":           lines(strings.Replace(header(plainQuery), `"1.0"`, `"2.0"`, 1), ev, end),
		"a version without minor": lines(strings.Replace(header(plainQuery), `"1.0"`, `"1"`, 1), ev, end),
		"no query":                lines(strings.Replace(header(plainQuery), `,"query":{"limit":1000}`, "", 1), ev, end),
		"an unknown query member": lines(header(`{"limit":1000,"since":"x"}`), ev, end),
		"an unknown type":         lines(header(plainQuery), `{"type":"note","offset":0}`, ev, end),
		"no type":                 lines(header(plainQuery), `{"offset":0}`, ev, end),
		"an unknown member":       lines(header(plainQuery), strings.Replace(ev, `"cursor":"c"`, `"cursor":"c","extra":1`, 1), end),
		"an event that is not an Event": lines(header(plainQuery),
			strings.Replace(ev, `"eventId":"e1"`, `"eventId":"e1","verdict":"ALLOW"`, 1), end),
		"an event of another major": lines(header(plainQuery),
			strings.Replace(ev, `"schemaVersion":"1.0"`, `"schemaVersion":"2.0"`, 1), end),
		"an event without a version": lines(header(plainQuery),
			strings.Replace(ev, `"schemaVersion":"1.0",`, ``, 1), end),
		"an event record without its event": lines(header(plainQuery), `{"type":"event","offset":0,"cursor":"c"}`, end),
		"an unknown gap reason":             lines(header(plainQuery), `{"type":"gap","offset":0,"cursor":"c","reason":"lost"}`, trailer(0, 1, 0, true)),
		"a record after the trailer":        lines(header(plainQuery), ev, end, ev),
		"a fragment after the trailer":      append(lines(header(plainQuery), ev, end), `{"type":"gap"`...),
		"counts that disagree":              lines(header(plainQuery), ev, trailer(2, 0, 0, true)),
		"a trailer without end_reached": lines(header(plainQuery), ev,
			strings.Replace(end, `"end_reached":true,`, ``, 1)),
		"a duplicate member": lines(header(plainQuery), strings.Replace(ev, `"type":"event"`, `"type":"event","type":"event"`, 1), end),
		"a null":             lines(header(plainQuery), strings.Replace(ev, `"cursor":"c"`, `"cursor":null`, 1), end),
		"not JSON":           lines(header(plainQuery), `{"type":`, end),
		"a trailing value":   lines(header(plainQuery), ev+`{}`, end),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			x, err := coverage.ReadExport(bytes.NewReader(b))
			if !errors.Is(err, coverage.ErrExport) || x != nil {
				t.Fatalf("ReadExport = %+v, %v; want ErrExport", x, err)
			}
		})
	}
}

// TestExportLineBound: a record of exactly the bound reads, one byte more is
// refused.
func TestExportLineBound(t *testing.T) {
	gap := func(n int) string {
		base := `{"type":"gap","offset":0,"cursor":"","reason":"malformed"}`
		return strings.Replace(base, `"cursor":""`, `"cursor":"`+strings.Repeat("c", n-len(base))+`"`, 1)
	}
	const bound = 3*262144 + 4096
	for _, tc := range []struct {
		n  int
		ok bool
	}{{bound, true}, {bound + 1, false}} {
		line := gap(tc.n)
		if len(line) != tc.n {
			t.Fatalf("built a line of %d bytes, want %d", len(line), tc.n)
		}
		_, err := coverage.ReadExport(bytes.NewReader(lines(header(plainQuery), line, trailer(0, 1, 0, true))))
		if (err == nil) != tc.ok {
			t.Errorf("a %d-byte record: err %v, want accepted %v", tc.n, err, tc.ok)
		}
	}
}

// TestARefusedProposalOfNoScopeNamesNoCall: a source and its records with no
// tenant or project, and a refused proposal with none either, at the
// observation's ids: equal emptiness names no call, so the call is around
// the plane.
func TestARefusedProposalOfNoScopeNamesNoCall(t *testing.T) {
	unscoped := strings.ReplaceAll(refusedAs(proposal{}), `"projectId":"p1","tenantId":"t1"`, `"projectId":"","tenantId":""`)
	x := wholeExport(t, unscoped)
	d := descriptor("s1", selfReported)
	d.TenantId, d.ProjectId = "", ""
	rep := report("s1", at(-10*time.Second), at(-10*time.Second))
	rep.GetImportReport().TenantId, rep.GetImportReport().ProjectId = "", ""
	o := observedA.record()
	o.GetObservation().TenantId, o.GetObservation().ProjectId = "", ""
	p := mustMap(t, coverage.Input{
		Inventory: toolInventory(t),
		Planes:    []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)), x)},
		Sources:   []coverage.Source{{Descriptor: d, Records: []*observev1.Record{rep, o}}},
	}).Paths[0]
	if len(p.Joins) != 1 || p.Joins[0].Join != coverage.JoinAround {
		t.Fatalf("joins %+v, want one call around the plane", p.Joins)
	}
}

// refusedAs is p at obs-a's trace and span, its envelope refused for want of
// a request id.
func refusedAs(p proposal) string {
	p.trace, p.span = traceA, spanA
	return strings.Replace(p.line(), `"requestId":"r1",`, ``, 1)
}

// TestAProposalThatDoesNotValidateLeavesItsJoinNotChecked: a plane records a
// call the contract refuses under the ids the agent sent, so such a proposal
// neither joins an observation nor lets it be a call around the plane, and
// the export holding it still reads whole. A call nothing classifies, the one
// refusal a plane records by design for a call it may let run, still joins.
func TestAProposalThatDoesNotValidateLeavesItsJoinNotChecked(t *testing.T) {
	const why = "the record of a call at its span in the export of plane a does not validate"
	ev := proposal{trace: traceA, span: spanA}.line()
	unclassified := proposal{trace: traceA, span: spanA, unclassified: true}.line()
	noRequest := strings.Replace(ev, `"requestId":"r1",`, ``, 1)
	const badHost = `"destination":{"host":"files..example"},"resource":`
	child := obs{id: "obs-child", kind: observev1.SubjectKind_SUBJECT_KIND_MODEL, name: "m", trace: traceA, span: spanB, parent: spanA}
	cases := []struct {
		name    string
		events  []string
		records []*observev1.Record
		want    coverage.Join
		why     string
	}{
		{"a valid proposal", []string{ev}, nil, coverage.Joined, ""},
		{"an unclassified proposal", []string{unclassified}, nil, coverage.Joined, ""},
		{"no request id", []string{noRequest}, nil, coverage.JoinNotChecked, why},
		{"no principal", []string{strings.Replace(ev, `"principal":{"id":"agent-1"},`, ``, 1)}, nil, coverage.JoinNotChecked, why},
		{"no envelope schema version", []string{strings.Replace(ev, `"proposed":{"schemaVersion":"1.0",`, `"proposed":{`, 1)}, nil,
			coverage.JoinNotChecked, why},
		{"unclassified and no request id", []string{strings.Replace(unclassified, `"requestId":"r1",`, ``, 1)}, nil,
			coverage.JoinNotChecked, why},
		{"a destination host refused", []string{strings.Replace(ev, `"resource":`, badHost, 1)}, nil, coverage.JoinNotChecked, why},
		{"unclassified, its destination host unchecked", []string{strings.Replace(unclassified, `"resource":`, badHost, 1)}, nil,
			coverage.Joined, ""},
		{"refused at a descendant's span", []string{strings.Replace(proposal{trace: traceA, span: spanB}.line(), `"requestId":"r1",`, ``, 1)},
			[]*observev1.Record{child.record()}, coverage.JoinNotChecked, why},
		{"refused at a span of another trace", []string{strings.Replace(proposal{trace: traceB, span: spanA}.line(), `"requestId":"r1",`, ``, 1)},
			nil, coverage.JoinAround, ""},
		{"refused with no span id", []string{strings.Replace(proposal{trace: traceA}.line(), `"requestId":"r1",`, ``, 1)}, nil,
			coverage.JoinAround, ""},
		{"refused beside a valid proposal", []string{noRequest, ev}, nil, coverage.Joined, ""},
		{"refused for another tool", []string{refusedAs(proposal{tool: "delete_issue"})}, nil, coverage.JoinAround, ""},
		{"refused on another upstream", []string{refusedAs(proposal{upstream: "gitlab"})}, nil, coverage.JoinAround, ""},
		{"refused in another tenant", []string{refusedAs(proposal{tenant: "t2"})}, nil, coverage.JoinAround, ""},
		{"refused in another project", []string{refusedAs(proposal{envelopeProject: "p2"})}, nil, coverage.JoinAround, ""},
		{"refused as a prompt", []string{refusedAs(proposal{kind: "prompt"})}, nil, coverage.JoinAround, ""},
		{"refused without a tool name", []string{strings.Replace(ev, `"name":"create_issue",`, ``, 1)}, nil, coverage.JoinAround, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := lines(append(append([]string{header(plainQuery), windowEvent(-time.Hour), windowEvent(0)}, tc.events...),
				trailer(len(tc.events)+2, 0, 0, true))...)
			x, err := coverage.ReadExport(bytes.NewReader(b))
			if err != nil || !x.Whole {
				t.Fatalf("%+v, %v; want a whole export", x, err)
			}
			if j := joinOf(t, x, append([]*observev1.Record{observedA.record()}, tc.records...)...); j.Join != tc.want || j.Why != tc.why {
				t.Fatalf("%+v, want %v (%q)", j, tc.want, tc.why)
			}
		})
	}
}
