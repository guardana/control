package coverage_test

import (
	"errors"
	"testing"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/coverage"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	selfReported = observev1.Trust_TRUST_SELF_REPORTED
	platform     = observev1.Trust_TRUST_PLATFORM
	independent  = observev1.Trust_TRUST_INDEPENDENT
)

func sourceState(t *testing.T, sources ...coverage.Source) coverage.PathCoverage {
	t.Helper()
	return mustMap(t, coverage.Input{Inventory: toolInventory(t), Sources: sources}).Paths[0]
}

func TestObservedNamesTheTrust(t *testing.T) {
	p := sourceState(t, liveSource(selfReported, obs{}.record()))
	if p.State != coverage.Observed || p.Trust != selfReported {
		t.Fatalf("%v at %v, want observed self-reported; %+v", p.State, p.Trust, p.Sources)
	}
	p = sourceState(t, liveSource(platform, obs{trust: platform}.record()))
	if p.State != coverage.Observed || p.Trust != platform {
		t.Fatalf("%v at %v, want observed at platform; %+v", p.State, p.Trust, p.Sources)
	}
}

// TestTheWeakerTrustIsPrinted: a descriptor lowered below what its records
// carry, or a record below its descriptor, prints the weaker one.
func TestTheWeakerTrustIsPrinted(t *testing.T) {
	cases := []struct {
		name               string
		descriptor, record observev1.Trust
		want               observev1.Trust
	}{
		{"a lowered descriptor", selfReported, platform, selfReported},
		{"a lower record", platform, selfReported, selfReported},
		{"an undeclared record trust reads as self-reported", platform, observev1.Trust(99), selfReported},
		{"both independent", independent, independent, independent},
	}
	for _, tc := range cases {
		p := sourceState(t, liveSource(tc.descriptor, obs{trust: tc.record}.record()))
		if p.State != coverage.Observed || p.Trust != tc.want || p.Sources[0].Trust != tc.want {
			t.Errorf("%s: %v at %v (line %v), want observed at %v", tc.name, p.State, p.Trust, p.Sources[0].Trust, tc.want)
		}
	}
}

func TestTheWeakestSourceTrustIsThePaths(t *testing.T) {
	inv := mustInventory(t, `{"schema_version":"0.1","paths":[{"id":"gh-create","kind":"mcp_tool","upstream":"github",`+
		`"tool":"create_issue","sources":[{"source_id":"s1","name":"create_issue"},{"source_id":"s2","name":"create_issue"}]}]}`)
	s2 := coverage.Source{Descriptor: descriptor("s2", selfReported), Records: []*observev1.Record{
		report("s2", at(-time.Second), at(-time.Second)), obs{source: "s2"}.record()}}
	p := mustMap(t, coverage.Input{Inventory: inv, Sources: []coverage.Source{liveSource(platform, obs{trust: platform}.record()), s2}}).Paths[0]
	if p.State != coverage.Observed || p.Trust != selfReported {
		t.Fatalf("%v at %v, want observed at self_reported", p.State, p.Trust)
	}
}

// TestLiveness measures the source's last heard time, the newest event time
// of a report taken no later than that report's receive time, to now.
func TestLiveness(t *testing.T) {
	heard := func(received, latest *timestamppb.Timestamp) coverage.Source {
		return coverage.Source{Descriptor: descriptor("s1", selfReported),
			Records: []*observev1.Record{report("s1", received, latest), obs{}.record()}}
	}
	cases := []struct {
		name string
		src  coverage.Source
		want coverage.State
	}{
		{"heard exactly a heartbeat ago", heard(at(-300*time.Second), at(-300*time.Second)), coverage.Observed},
		{"heard a heartbeat and a second ago", heard(at(-301*time.Second), at(-301*time.Second)), coverage.Unknown},
		{"heard now", heard(at(0), at(0)), coverage.Observed},
		{"heard a second after now", heard(at(time.Second), at(time.Second)), coverage.Unknown},
		{"an event time after the receive time is taken at the receive time", heard(at(-10*time.Second), at(time.Hour)), coverage.Observed},
		{"an old receive time caps a fresh event time", heard(at(-time.Hour), at(-time.Second)), coverage.Unknown},
		{"a report with no event time does not count", heard(at(-time.Second), nil), coverage.Unknown},
		{"a report with no receive time does not count", heard(nil, at(-time.Second)), coverage.Unknown},
		{"a log with no report", coverage.Source{Descriptor: descriptor("s1", selfReported), Records: []*observev1.Record{obs{}.record()}}, coverage.Unknown},
		{"a log not written yet", coverage.Source{Descriptor: descriptor("s1", selfReported)}, coverage.Unknown},
		{"the newest of two reports", coverage.Source{Descriptor: descriptor("s1", selfReported), Records: []*observev1.Record{
			report("s1", at(-time.Second), at(-time.Second)), report("s1", at(-time.Hour), at(-time.Hour)), obs{}.record()}}, coverage.Observed},
		{"a newer report with no event time leaves the older one", coverage.Source{Descriptor: descriptor("s1", selfReported), Records: []*observev1.Record{
			report("s1", at(-time.Hour), at(-time.Hour)), report("s1", at(-time.Second), nil), obs{}.record()}}, coverage.Unknown},
	}
	for _, tc := range cases {
		if p := sourceState(t, tc.src); p.State != tc.want {
			t.Errorf("%s: %v, want %v; %+v", tc.name, p.State, tc.want, p.Sources)
		}
	}
}

func TestAnotherSourcesRecordsAreIgnored(t *testing.T) {
	cases := []struct {
		name string
		src  coverage.Source
		want coverage.State
	}{
		{"its observation", coverage.Source{Descriptor: descriptor("s1", selfReported), Records: []*observev1.Record{
			report("s1", at(-time.Second), at(-time.Second)), obs{source: "s2"}.record()}}, coverage.NotCovered},
		{"its report", coverage.Source{Descriptor: descriptor("s1", selfReported), Records: []*observev1.Record{
			report("s2", at(-time.Second), at(-time.Second)), obs{}.record()}}, coverage.Unknown},
	}
	for _, tc := range cases {
		if p := sourceState(t, tc.src); p.State != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, p.State, tc.want)
		}
	}
}

// TestRecordsOfAnotherTenantOrProjectAreIgnored: a log written under one
// tenant or project counts nothing for a descriptor pointed at another.
func TestRecordsOfAnotherTenantOrProjectAreIgnored(t *testing.T) {
	reportIn := func(tenant, project string) *observev1.Record {
		r := report("s1", at(-time.Second), at(-time.Second))
		r.GetImportReport().TenantId, r.GetImportReport().ProjectId = tenant, project
		return r
	}
	cases := []struct {
		name    string
		records []*observev1.Record
		want    coverage.State
	}{
		{"both in its tenant and project", []*observev1.Record{reportIn("t1", "p1"), obs{}.record()}, coverage.Observed},
		{"a report of another tenant", []*observev1.Record{reportIn("t2", "p1"), obs{}.record()}, coverage.Unknown},
		{"a report of another project", []*observev1.Record{reportIn("t1", "p2"), obs{}.record()}, coverage.Unknown},
		{"an observation of another tenant", []*observev1.Record{reportIn("t1", "p1"), obs{tenant: "t2"}.record()}, coverage.NotCovered},
		{"an observation of another project", []*observev1.Record{reportIn("t1", "p1"), obs{project: "p2"}.record()}, coverage.NotCovered},
	}
	for _, tc := range cases {
		src := coverage.Source{Descriptor: descriptor("s1", selfReported), Records: tc.records}
		if p := sourceState(t, src); p.State != tc.want {
			t.Errorf("%s: %v, want %v; %+v", tc.name, p.State, tc.want, p.Sources)
		}
	}
}

func TestOnlyAToolObservationOfTheNameMatches(t *testing.T) {
	cases := map[string]obs{
		"an agent of the tool's name":   {kind: observev1.SubjectKind_SUBJECT_KIND_AGENT},
		"a model of the tool's name":    {kind: observev1.SubjectKind_SUBJECT_KIND_MODEL},
		"an undeclared subject kind":    {kind: observev1.SubjectKind(9)},
		"another tool":                  {name: "create_issues"},
		"the tool's name in other case": {name: "Create_issue"},
	}
	for name, o := range cases {
		if p := sourceState(t, liveSource(selfReported, o.record())); p.State != coverage.NotCovered {
			t.Errorf("%s: %v, want not covered", name, p.State)
		}
	}
}

func TestServerAddressNarrowsTheMatch(t *testing.T) {
	inv := mustInventory(t, `{"schema_version":"0.1","paths":[{"id":"gh-create","kind":"mcp_tool","upstream":"github",`+
		`"tool":"create_issue","sources":[{"source_id":"s1","name":"create_issue","server_address":"api.github.com"}]}]}`)
	for _, tc := range []struct {
		server string
		want   coverage.State
	}{{"api.github.com", coverage.Observed}, {"evil.example", coverage.NotCovered}, {"", coverage.NotCovered}} {
		p := mustMap(t, coverage.Input{Inventory: inv, Sources: []coverage.Source{liveSource(selfReported, obs{server: tc.server}.record())}}).Paths[0]
		if p.State != tc.want {
			t.Errorf("server %q: %v, want %v", tc.server, p.State, tc.want)
		}
	}
}

// TestARemovedDescriptorIsNotCovered: a source the inventory names but the
// caller could not pass, because its descriptor is gone, covers nothing.
func TestARemovedDescriptorIsNotCovered(t *testing.T) {
	p := sourceState(t)
	if p.State != coverage.NotCovered || len(p.Sources) != 1 || p.Sources[0].State != coverage.NotCovered || p.Sources[0].Basis == "" {
		t.Fatalf("%v with %+v, want not covered and a line saying why", p.State, p.Sources)
	}
	other := coverage.Source{Descriptor: descriptor("s9", selfReported), Records: []*observev1.Record{
		report("s9", at(-time.Second), at(-time.Second)), obs{source: "s9"}.record()}}
	if p := sourceState(t, other); p.State != coverage.NotCovered {
		t.Fatalf("a source the path does not name: %v, want not covered", p.State)
	}
}

func TestALiveSourceThatSawNothingDoesNotCover(t *testing.T) {
	if p := sourceState(t, liveSource(selfReported)); p.State != coverage.NotCovered || p.Sources[0].Basis == "" {
		t.Fatalf("%v with %+v, want not covered and a line saying why", p.State, p.Sources)
	}
}

func TestMapRefusesItsSources(t *testing.T) {
	cases := map[string][]coverage.Source{
		"no descriptor":  {{}},
		"a source twice": {liveSource(selfReported), liveSource(platform)},
	}
	for name, s := range cases {
		r, err := coverage.Map(coverage.Input{Inventory: toolInventory(t), Sources: s, Now: now})
		if !errors.Is(err, coverage.ErrInput) || r != nil {
			t.Errorf("%s: Map = %+v, %v; want ErrInput", name, r, err)
		}
	}
	for name, in := range map[string]coverage.Input{
		"no inventory": {Now: now},
		"no path":      {Inventory: &coverage.Inventory{}, Now: now},
		"a path twice": {Inventory: &coverage.Inventory{Paths: []coverage.Path{{ID: "a", Kind: "egress", Host: "h"},
			{ID: "a", Kind: "egress", Host: "i"}}}, Now: now},
		"no time": {Inventory: toolInventory(t)},
	} {
		if r, err := coverage.Map(in); !errors.Is(err, coverage.ErrInput) || r != nil {
			t.Errorf("%s: Map = %+v, %v; want ErrInput", name, r, err)
		}
	}
}

// TestASourceNotPassedSaysWhetherADescriptorWasAbsent: a source the
// inventory names and no Source carries was either never given or given as a
// descriptor that does not exist; the line says which of these it can be.
func TestASourceNotPassedSaysWhetherADescriptorWasAbsent(t *testing.T) {
	cases := []struct {
		name   string
		absent []string
		basis  string
	}{
		{"no descriptor absent", nil, "no --source given for it"},
		{"one descriptor absent", []string{"/etc/src.json"},
			"no --source given for it, or its descriptor is absent: /etc/src.json; a removed descriptor covers nothing"},
		{"two descriptors absent", []string{"a.json", "b\x1b.json"},
			`no --source given for it, or its descriptor is absent: a.json, "b\x1b.json"; a removed descriptor covers nothing`},
	}
	for _, tc := range cases {
		p := mustMap(t, coverage.Input{Inventory: toolInventory(t), AbsentDescriptors: tc.absent}).Paths[0]
		if len(p.Sources) != 1 || p.Sources[0].State != coverage.NotCovered || p.Sources[0].Basis != tc.basis {
			t.Errorf("%s: %+v, want not covered because %q", tc.name, p.Sources, tc.basis)
		}
	}
}
