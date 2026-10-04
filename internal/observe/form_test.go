package observe_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
)

// runeChunk is how many runes one swept line carries: enough to take every
// rune through MarshalLine in about a thousand lines, few enough that the
// most escaped chunk stays under MaxLineBytes.
const runeChunk = 2000

// TestEveryLineTheWriterWritesIsInItsForm: every rune, in the free text of
// an observation and in a map key of a report, written by MarshalLine,
// passes the form check, and so do both fixture lines with and without their
// newline.
func TestEveryLineTheWriterWritesIsInItsForm(t *testing.T) {
	chunk := make([]rune, 0, runeChunk)
	lines := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if utf8.ValidRune(r) {
			chunk = append(chunk, r)
		}
		if len(chunk) == runeChunk || (r == utf8.MaxRune && len(chunk) > 0) {
			lines += checkWrittenForm(t, chunk)
			chunk = chunk[:0]
		}
	}
	if want := 2 * ((0x10f800 + runeChunk - 1) / runeChunk); lines != want {
		t.Fatalf("%d lines were checked, want %d", lines, want)
	}
	for _, line := range fixtureLines(t) {
		for _, l := range [][]byte{line, bytes.TrimSuffix(line, []byte("\n"))} {
			if err := observe.CheckLineForm(l); err != nil {
				t.Errorf("the fixture line %.40q is refused: %v", l, err)
			}
		}
	}
}

// checkWrittenForm writes chunk as an observation's text and as a report's
// map key, checks both lines' form and returns how many it checked.
func checkWrittenForm(t *testing.T, chunk []rune) int {
	t.Helper()
	o := withName(string(chunk))
	o.Subject.Operation = string(chunk[:len(chunk)/2])
	r := fixtureReport()
	r.Counts.Skipped = map[string]uint64{string(chunk): 1, "unmapped_operation": 2}
	for _, rec := range []*observev1.Record{obsRecord(o), reportRecord(r)} {
		line, err := observe.MarshalLine(rec)
		if err != nil {
			t.Fatalf("runes %U to %U: MarshalLine = %v", chunk[0], chunk[len(chunk)-1], err)
		}
		if err := observe.CheckLineForm(line); err != nil {
			t.Fatalf("runes %U to %U: the writer's own line is refused: %v", chunk[0], chunk[len(chunk)-1], err)
		}
	}
	return 2
}

// TestTheFormCheckRefusesWhatTheWriterDoesNotWrite: each case is the
// observation fixture line with one spelling changed that the strict reader
// still reads, so only the form check tells it from the writer's line.
func TestTheFormCheckRefusesWhatTheWriterDoesNotWrite(t *testing.T) {
	obs, rep := string(fixtureLines(t)[0]), string(fixtureLines(t)[1])
	cases := map[string]string{
		"raw C1":                        strings.Replace(obs, "read_file", "read\u009bfile", 1),
		"raw DEL":                       strings.Replace(obs, "read_file", "read\x7ffile", 1),
		"raw NEL":                       strings.Replace(obs, "read_file", "read\u0085file", 1),
		"raw bidi override":             strings.Replace(obs, "read_file", "read\u202efile", 1),
		"raw line separator":            strings.Replace(obs, "read_file", "read\u2028file", 1),
		"snake_case member":             strings.Replace(obs, `"schemaVersion"`, `"schema_version"`, 1),
		"snake_case nested member":      strings.Replace(obs, `"traceId"`, `"trace_id"`, 1),
		"space after a colon":           strings.Replace(obs, `"tenantId":`, `"tenantId": `, 1),
		"space before a colon":          strings.Replace(obs, `"tenantId":`, `"tenantId" :`, 1),
		"space after a comma":           strings.Replace(obs, `,"projectId"`, `, "projectId"`, 1),
		"tab between tokens":            strings.Replace(obs, `{"observation":`, "{\t\"observation\":", 1),
		"newline inside":                strings.Replace(obs, `,"projectId"`, ",\n\"projectId\"", 1),
		"trailing space":                strings.TrimSuffix(obs, "\n") + " \n",
		"escaped letter":                strings.Replace(obs, "read_file", "re\\u0061d_file", 1),
		"escaped slash":                 strings.Replace(obs, "read_file", `read\/file`, 1),
		"a short form not the writer's": strings.Replace(obs, "read_file", `read\/2028file`, 1),
		"capital hex escape":            strings.Replace(obs, "read_file", "read\\u202Efile", 1),
		"long form of a short escape":   strings.Replace(obs, "read_file", `read\u000afile`, 1),
		"escaped quote as \\u":          strings.Replace(obs, "read_file", "read\\u0022file", 1),
		"escaped member name":           strings.Replace(obs, `"tenantId"`, `"tenant\u0049d"`, 1),
		"raw C1 in a map key":           strings.Replace(rep, "unmapped_operation", "unmapped\u0085operation", 1),
		"snake_case beside a map":       strings.Replace(rep, `"otherResource"`, `"other_resource"`, 1),
		"two newlines":                  obs + "\n",
		"a carriage return before \\n":  strings.TrimSuffix(obs, "\n") + "\r\n",
	}
	for name, line := range cases {
		if line == obs || line == rep {
			t.Fatalf("%s: the edit changed nothing", name)
		}
		if err := observe.CheckLineForm([]byte(line)); !errors.Is(err, observe.ErrLineForm) {
			t.Errorf("%s: CheckLineForm = %v, want ErrLineForm", name, err)
		}
	}
}

// TestTheEditsStillRead: the refused spellings above are ones the strict
// reader takes, which is why the form check exists at all.
func TestTheEditsStillRead(t *testing.T) {
	obs := string(fixtureLines(t)[0])
	for _, line := range []string{
		strings.Replace(obs, "read_file", "read\u009bfile", 1),
		strings.Replace(obs, `"schemaVersion"`, `"schema_version"`, 1),
		strings.Replace(obs, `"tenantId":`, `"tenantId": `, 1),
		strings.Replace(obs, "read_file", `read\/file`, 1),
	} {
		if _, err := observe.UnmarshalLine([]byte(line)); err != nil {
			t.Errorf("UnmarshalLine(%.60q) = %v; the form check is then not what refuses it", line, err)
		}
	}
}

// A map's keys are data, so a snake_case key and an escaped one the writer
// writes are in the form.
func TestAMapKeyIsNotAMemberName(t *testing.T) {
	rep := string(fixtureLines(t)[1])
	for _, key := range []string{"unmapped_operation", `a\u0085b`, `x\ny`, `q\"q`} {
		line := strings.Replace(rep, "unmapped_operation", key, 1)
		if err := observe.CheckLineForm([]byte(line)); err != nil {
			t.Errorf("map key %s: CheckLineForm = %v", key, err)
		}
	}
}

// Broken bytes are refused, never passed and never a panic.
func TestTheFormCheckRefusesBrokenBytes(t *testing.T) {
	for _, line := range []string{`{"a`, `{"name":"\`, `{"name":"\u00`, `{"name":"\u00zz"}`, "{\"name\":\"\xff\"}", "\xc2\x85{}", `{"observation":"x"` + "\x01" + `}`} {
		if err := observe.CheckLineForm([]byte(line)); !errors.Is(err, observe.ErrLineForm) {
			t.Errorf("%q: CheckLineForm = %v, want ErrLineForm", line, err)
		}
	}
}
