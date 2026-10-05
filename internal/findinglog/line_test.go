package findinglog

import (
	"errors"
	"strings"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// TestALineIsCompactWithTheRunesAReaderMistakesEscaped: DEL, a C1 control,
// U+2028, U+2029 and a bidirectional control each leave the writer as a
// six-byte escape, and the line reads back as the record it was.
func TestALineIsCompactWithTheRunesAReaderMistakesEscaped(t *testing.T) {
	f := finding(idA, confirmed, "a\u007fb\u0085c\u2028d\u2029e\u202ef")
	got := line(t, findingRecord(f))
	if want := `"ruleId":"a\u007fb\u0085c\u2028d\u2029e\u202ef"`; !strings.Contains(got, want) {
		t.Errorf("line %q does not hold %s", got, want)
	}
	if !strings.HasPrefix(got, `{"findingRecord":{"schemaVersion":"0.1","tenantId":"tenant-a",`) || !strings.HasSuffix(got, "}}\n") {
		t.Errorf("line %q is not one compact record and its newline", got)
	}
	if strings.Count(got, "\n") != 1 || strings.Contains(got, " ") {
		t.Errorf("line %q is not compact", got)
	}
	back, err := unmarshalLine([]byte(got))
	if err != nil || !proto.Equal(back, findingRecord(f)) {
		t.Errorf("unmarshalLine = %v, %v; want the record written", back, err)
	}
}

// TestUnmarshalLineRefusesWhatTheWriterDoesNotWrite: each line is a line the
// writer wrote with one change.
func TestUnmarshalLineRefusesWhatTheWriterDoesNotWrite(t *testing.T) {
	a := line(t, findingRecord(finding(idA, confirmed, "repeated_denial")))
	r := line(t, writtenReport(1))
	for name, bad := range map[string]string{
		"an unknown member":              strings.Replace(a, `"tenantId":`, `"planted":"x","tenantId":`, 1),
		"an unknown member in a report":  strings.Replace(r, `"tenantId":`, `"planted":"x","tenantId":`, 1),
		"a null member":                  strings.Replace(a, `"procedure":{`, `"planted":null,"procedure":{`, 1),
		"a null required member":         strings.Replace(a, `"tenantId":"tenant-a"`, `"tenantId":null`, 1),
		"a null member nothing requires": strings.Replace(a, `"ruleVersion":"1"`, `"ruleVersion":null`, 1),
		"a member twice":                 strings.Replace(a, `"tenantId":"tenant-a"`, `"tenantId":"tenant-a","tenantId":"tenant-b"`, 1),
		"two records in one line":        strings.Replace(a, `{"findingRecord":`, `{"superviseReport":{},"findingRecord":`, 1),
		"no record":                      "{}\n",
		"a snake_case member":            strings.Replace(a, `"tenantId"`, `"tenant_id"`, 1),
		"a space outside a string":       strings.Replace(a, `"tenantId":`, `"tenantId": `, 1),
		"a raw C1 control":               strings.Replace(a, "repeated_denial", "repeated\u0085denial", 1),
		"a raw bidirectional control":    strings.Replace(a, "repeated_denial", "repeated\u202edenial", 1),
		"an escape the writer never":     strings.Replace(a, "repeated_denial", `repeated\u0041denial`, 1),
		"an upper-case escape":           strings.Replace(a, "repeated_denial", `repeated\u202Edenial`, 1),
		"a finding of version 0.2":       strings.Replace(a, `"schemaVersion":"0.1"`, `"schemaVersion":"0.2"`, 1),
		"a report of version 0.2":        strings.Replace(r, `"schemaVersion":"0.1"`, `"schemaVersion":"0.2"`, 1),
		"a finding of no version":        strings.Replace(a, `"schemaVersion":"0.1",`, ``, 1),
		"a finding id of another form":   strings.Replace(a, idA, "fnd-0123", 1),
		"a report of another run's form": strings.Replace(r, runID, "run-1", 1),
		"a trailing value":               strings.TrimSuffix(a, "\n") + "{}\n",
		"blank":                          "\n",
	} {
		if bad == a || bad == r {
			t.Fatalf("%s: the change did not apply", name)
		}
		if got, err := unmarshalLine([]byte(bad)); err == nil || got != nil {
			t.Errorf("%s: unmarshalLine(%q) = %v, %v; want a refusal", name, bad, got, err)
		}
	}
}

// padded is a's line with its rule id grown until the line, newline left
// out, is n bytes long.
func padded(t testing.TB, n int) *findingv1alpha1.Record {
	t.Helper()
	base := len(line(t, findingRecord(finding(idA, confirmed, "r")))) - 1
	return findingRecord(finding(idA, confirmed, strings.Repeat("r", 1+n-base)))
}

// TestTheWriterTakesALineOfTheBoundAndNoMore: the bound counts the line's
// bytes after escaping, its newline left out.
func TestTheWriterTakesALineOfTheBoundAndNoMore(t *testing.T) {
	at, err := marshalLine(padded(t, MaxLineBytes))
	if err != nil || len(at) != MaxLineBytes+1 {
		t.Fatalf("a line of %d bytes: marshalLine = %d bytes, %v", MaxLineBytes, len(at), err)
	}
	if _, err := unmarshalLine(at); err != nil {
		t.Errorf("the reader refuses a line of the bound: %v", err)
	}
	if got, err := marshalLine(padded(t, MaxLineBytes+1)); !errors.Is(err, errLineTooLong) || got != nil {
		t.Errorf("a line of %d bytes: marshalLine = %d bytes, %v; want errLineTooLong", MaxLineBytes+1, len(got), err)
	}
	over := strings.Replace(string(at), `"ruleId":"r`, `"ruleId":"rr`, 1)
	if got, err := unmarshalLine([]byte(over)); !errors.Is(err, errLineTooLong) || got != nil {
		t.Errorf("a line of %d bytes: unmarshalLine = %v, %v; want errLineTooLong", len(over)-1, got, err)
	}
}

// TestTheWriterRefusesAFieldItCannotName: protojson would drop it without a
// word, in a singular message and in a list's alike.
func TestTheWriterRefusesAFieldItCannotName(t *testing.T) {
	unknown := []byte{0xf8, 0x01, 0x01} // field 31, varint 1
	top := finding(idA, confirmed, "repeated_denial")
	top.ProtoReflect().SetUnknown(unknown)
	inner := finding(idA, confirmed, "repeated_denial")
	inner.Finding.ProtoReflect().SetUnknown(unknown)
	listed := finding(idA, confirmed, "repeated_denial")
	listed.Refs[0].GetEvent().ProtoReflect().SetUnknown(unknown)
	rule := report()
	rule.Rules[0].ProtoReflect().SetUnknown(unknown)
	for name, r := range map[string]*findingv1alpha1.Record{
		"the record":        findingRecord(top),
		"its finding":       findingRecord(inner),
		"a listed ref":      findingRecord(listed),
		"a report's rule":   reportRecord(rule),
		"a record with nil": {},
	} {
		if got, err := marshalLine(r); err == nil || got != nil {
			t.Errorf("%s: marshalLine = %q, %v; want a refusal", name, got, err)
		}
	}
}
