package main

import (
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const canary = "CANARY-5d1e"

// printable names the string fields the report may print or keep: the ids,
// the action's kind, name, protocol and provider, the decision's id and
// reason codes, and the event's schema version, which has to stay one this
// reader reads. Every other string, bytes and map field holds the canary.
var printable = func() map[protoreflect.FullName]bool {
	m := map[protoreflect.FullName]bool{}
	for _, f := range []string{
		"Event.event_id",
		"Event.request_id",
		"Event.run_id",
		"Event.project_id",
		"Event.tenant_id",
		"Event.schema_version",
		"Event.execution_id",
		"Event.prev_event_id",
		"Action.kind",
		"Action.name",
		"Action.protocol",
		"Action.provider",
		"Decision.decision_id",
		"Decision.reason_codes",
	} {
		m[protoreflect.FullName(contractPackage+"."+f)] = true
	}
	return m
}()

// withCanary fills every field of m that printable does not name with the
// canary, through its descriptor, and returns how many it filled. Of a oneof
// only the member set is filled, which is the event's payload.
func withCanary(m protoreflect.Message) int {
	n := 0
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		f := fields.Get(i)
		if o := f.ContainingOneof(); o != nil && !o.IsSynthetic() && !m.Has(f) {
			continue
		}
		n += fillField(m, f)
	}
	return n
}

func fillField(m protoreflect.Message, f protoreflect.FieldDescriptor) int {
	switch {
	case f.IsMap():
		key := protoreflect.ValueOfString(canary).MapKey()
		switch f.MapValue().Kind() {
		case protoreflect.StringKind:
			m.Mutable(f).Map().Set(key, protoreflect.ValueOfString(canary))
		default:
			m.Mutable(f).Map().Set(key, protoreflect.ValueOfInt64(1))
		}
		return 1
	case f.IsList() && f.Kind() == protoreflect.MessageKind:
		list := m.Mutable(f).List()
		e := list.NewElement()
		list.Append(e)
		return withCanary(e.Message())
	case printable[f.FullName()]:
		return 0
	case f.IsList() && f.Kind() == protoreflect.StringKind:
		m.Mutable(f).List().Append(protoreflect.ValueOfString(canary))
		return 1
	case f.Kind() == protoreflect.StringKind:
		m.Set(f, protoreflect.ValueOfString(canary))
		return 1
	case f.Kind() == protoreflect.BytesKind:
		m.Set(f, protoreflect.ValueOfBytes([]byte(canary)))
		return 1
	case f.Kind() == protoreflect.MessageKind && string(f.Message().FullName().Parent()) == contractPackage:
		return withCanary(m.Mutable(f).Message())
	}
	return 0
}

// canaried rewrites each event line with the canary in every field the
// report may not print, and returns the lines and the fields filled.
func canaried(t *testing.T, lines []string) ([]string, int) {
	t.Helper()
	out := make([]string, len(lines))
	filled := 0
	for i, l := range lines {
		ev := &controlv1.Event{}
		if err := protojson.Unmarshal([]byte(l), ev); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		filled += withCanary(ev.ProtoReflect())
		b, err := protojson.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(b)
	}
	return out, filled
}

func finding() string  { return `,"finding":{"findingId":"x"}` }
func reloaded() string { return `,"policy":{"bundleId":"x"}` }

// TestPrivacy runs the state mode over requests that reach every alert and
// every end, with every field it may not print holding the canary, and finds
// the canary in none of stdout, stderr, the alert log and the state.
func TestPrivacy(t *testing.T) {
	approvedFinding := then(approved, step{"FINDING_RAISED", finding()}, step{"POLICY_RELOADED", reloaded()})
	paused := []step{{"ACTION_PROPOSED", proposed("read_order")}, {"POLICY_DECIDED", decidedAs("k1", "ALLOW", "RULE_ALLOW")},
		{"ACTION_BLOCKED", decidedAs("p1", "DENY", "PAUSED")}}
	lines, filled := canaried(t, concat(chain("a", approvedFinding...), chain("p", paused...), chain("e", expired...),
		chain("u", undetermined...), without(chain("m", allowed...), 1), chain("o", held...)))
	conflicting, more := canaried(t, []string{evt("a", "a-2", "a-1", "POLICY_DECIDED", decided("DENY", "RULE_DENY"))})
	if filled+more < 100 {
		t.Fatalf("the canary went into %d fields, which leaves the test examining little", filled+more)
	}
	dir := newState(t)
	var out strings.Builder
	first := framed("c.trail", lines, 0, len(lines), 1000)
	all := append(append([]string(nil), lines...), conflicting...)
	second := framed("c.trail", all, len(lines), len(all), 1000)
	gap := strings.Replace(second, `{"type":"trailer"`, `{"type":"gap","offset":999999,"cursor":"v1:`+digest(lines[0]+"\n")+`:1000000:`+digest("\n")+`","reason":"`+canary+`"}`+"\n"+`{"type":"trailer"`, 1)
	gap = strings.Replace(gap, `"gap":0`, `"gap":1`, 1)
	for _, in := range []string{first, gap} {
		if !strings.Contains(in, canary) {
			t.Fatal("the input holds no canary")
		}
		got := consume(t, dir, in)
		if got.code != 1 {
			t.Fatalf("exit %d, want 1 with alerts; stderr:\n%s", got.code, got.stderr)
		}
		out.WriteString(got.stdout + got.stderr)
	}
	log := alertLog(t, dir)
	for _, code := range []string{"evidence_gap", "conflicting_event", "lifecycle_unknown", "indeterminate", "plane_block", "approval_expired"} {
		if !strings.Contains(log, `"alert":"`+code+`"`) {
			t.Errorf("no %s alert was raised, so its fields went unexamined:\n%s", code, log)
		}
	}
	if !strings.Contains(stateOf(t, dir), `"request":"o"`) {
		t.Error("the state holds no open request, so what it keeps of one went unexamined")
	}
	for where, s := range map[string]string{"stdout and stderr": out.String(), "alerts.jsonl": log, "state.json": stateOf(t, dir)} {
		if strings.Contains(s, canary) {
			t.Errorf("%s holds the canary:\n%s", where, s)
		}
	}
}

// refusedWithCanary is an export the reader refuses, or reads a record of
// only to refuse it, with the canary where a decoder's error would quote it.
type refusedWithCanary struct {
	name string
	in   func() string
}

func canaryRefusals() []refusedWithCanary {
	r1 := chain("r1", allowed...)
	inEvent := func(old, with string) func() string {
		return func() string {
			return newExport("1.0").event(r1[:3]...).event(strings.Replace(r1[3], old, with, 1)).whole()
		}
	}
	header := newExport("1.0").lines[0]
	withHeader := func(old, with string) func() string {
		return func() string {
			x := newExport("1.0").event(r1...)
			return strings.Replace(x.whole(), header, strings.Replace(header, old, with, 1), 1)
		}
	}
	record := func(line string) func() string {
		return func() string { return newExport("1.0").event(r1...).raw(line).whole() }
	}
	return []refusedWithCanary{
		{"a timestamp", inEvent(`"kind"`, `"occurredAt":"`+canary+`","kind"`)},
		{"an int64 of a decision", func() string {
			return newExport("1.0").event(r1[0], strings.Replace(r1[1], `"verdict"`, `"decisionLatencyUs":"`+canary+`","verdict"`, 1)).whole()
		}},
		{"an enum", inEvent(`"kind":"EVENT_KIND_ACTION_COMPLETED"`, `"kind":"`+canary+`"`)},
		{"a member the contract does not name", inEvent(`"kind"`, `"`+canary+`":1,"kind"`)},
		{"a schema version", inEvent(`"schemaVersion":"1.0"`, `"schemaVersion":"9`+canary+`"`)},
		{"a header version", withHeader(`"version":"1.0"`, `"version":"`+canary+`"`)},
		{"a header format", withHeader(`"format":"`+exportFormat+`"`, `"format":"`+canary+`"`)},
		{"a header type", withHeader(`"type":"header"`, `"type":"`+canary+`"`)},
		{"a header member", withHeader(`"file"`, `"`+canary+`":1,"file"`)},
		{"a header that is not JSON", withHeader(`{"type"`, `{`+canary+`"type"`)},
		{"a query after", withHeader(`"query":{`, `"query":{"after":"`+canary+`",`)},
		{"a header source", withHeader(`"source":"`+exportSource, `"source":"`+canary)},
		{"an event cursor", record(`{"type":"event","offset":900,"cursor":"` + canary + `","event":` + r1[0] + `}`)},
		{"a gap cursor", record(`{"type":"gap","offset":900,"cursor":"` + canary + `","reason":"malformed"}`)},
		{"a record type", record(`{"type":"` + canary + `","offset":900}`)},
		{"a record member", record(`{"type":"gap","offset":900,"reason":"malformed","` + canary + `":1}`)},
		{"a record that is not JSON", record(`{"type":"gap",` + canary + `}`)},
		{"a gap reason", func() string { return newExport("1.0").event(r1...).gap(canary).whole() }},
		{"a trailer cursor", func() string {
			x := newExport("1.0").event(r1...)
			return x.cut() + strings.Replace(x.trailerLine(false, 0), someCursor, canary, 1) + "\n"
		}},
		{"a trailer member", func() string {
			x := newExport("1.0").event(r1...)
			return x.cut() + strings.Replace(x.trailerLine(true, 0), `"writer_held"`, `"`+canary+`":1,"writer_held"`, 1) + "\n"
		}},
	}
}

// TestPrivacyOfRefusals: what a refusal says, in either mode, names no value
// the input gave, whichever decoder refused it.
func TestPrivacyOfRefusals(t *testing.T) {
	for _, tc := range canaryRefusals() {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in()
			if !strings.Contains(in, canary) {
				t.Fatal("the input holds no canary")
			}
			refused := false
			for mode, got := range map[string]outcome{"report": report(t, in), "state": consume(t, newState(t), in)} {
				refused = refused || got.code != 0
				if strings.Contains(got.stdout+got.stderr, canary) {
					t.Errorf("%s mode printed the canary:\n%s%s", mode, got.stdout, got.stderr)
				}
			}
			if !refused {
				t.Error("both modes took the input whole: the case examines no refusal")
			}
		})
	}
}
