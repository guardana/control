//go:build unix

package findinglog

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// logWithTail is a log holding one committed write, followed by tail, and
// the length of that write.
func logWithTail(t *testing.T, tail string) (string, int) {
	t.Helper()
	dir := logDir(t)
	l := openLog(t, dir)
	write(t, l, finding(idA, confirmed, "repeated_denial"))
	mustDo(t, l.Close())
	path := filepath.Join(dir, FileName)
	whole := len(contents(t, path))
	appendTo(t, path, tail)
	return dir, whole
}

// TestOpenCutsWhatACrashLeftAfterTheLastReport: the start of a line, a
// whole record without its newline, or whole findings with no report; none
// of it was reported written.
func TestOpenCutsWhatACrashLeftAfterTheLastReport(t *testing.T) {
	b := line(t, findingRecord(finding(idB, suspected, "outside")))
	r := line(t, writtenReport(1))
	for name, tail := range map[string]string{
		"an opening brace":            `{`,
		"the start of a finding":      `{"findingRecord":{"schemaVer`,
		"the start of a report":       `{"superviseReport":{"schemaVersion":"0.1","tenantId":"t`,
		"a finding with no newline":   strings.TrimSuffix(b, "\n"),
		"a finding and no report":     b,
		"a finding and a torn report": b + r[:40],
		"a report with no newline":    b + strings.TrimSuffix(r, "\n"),
	} {
		dir, whole := logWithTail(t, tail)
		l := openLog(t, dir)
		if got := len(contents(t, filepath.Join(dir, FileName))); got != whole {
			t.Errorf("%s: after Open the file holds %d bytes, want %d", name, got, whole)
		}
		if got := write(t, l, finding(idB, suspected, "outside")); got.Written != 1 {
			t.Errorf("%s: the cut finding was indexed: %+v", name, got)
		}
	}
}

// TestOpenRefusesAWholeLogNoWriterLeaves: the refused Open names the line
// and its offset, carries none of its bytes and changes nothing.
func TestOpenRefusesAWholeLogNoWriterLeaves(t *testing.T) {
	a := line(t, findingRecord(finding(idA, confirmed, "repeated_denial")))
	again := line(t, findingRecord(finding(idA, confirmed, "planted")))
	r := line(t, writtenReport(1))
	planted := strings.Replace(a, `"tenantId":`, `"planted":"x","tenantId":`, 1)
	for name, c := range map[string]struct {
		tail   string
		number int
	}{
		"an unknown member, committed":                {planted + r, 3},
		"an unknown member, uncommitted":              {planted, 3},
		"a key the log holds":                         {again + r, 3},
		"a key twice in one write":                    {line(t, findingRecord(finding(idB, suspected, "outside"))) + line(t, findingRecord(finding(idB, suspected, "planted"))), 4},
		"a report counting a finding its write lacks": {r, 3},
		"a report counting fewer than its write holds": {line(t, findingRecord(finding(idB, suspected, "outside"))) +
			line(t, writtenReport(0)), 4},
		"a carriage return":        {strings.Replace(r, "\n", "\r\n", 1), 3},
		"a line that is no record": {"{}\n" + r, 3},
		"a line over the bound":    {`{"findingRecord":"` + strings.Repeat("p", MaxLineBytes) + "\"}\n" + r, 3},
	} {
		dir, whole := logWithTail(t, c.tail)
		l, err := Open(dir)
		if !errors.Is(err, ErrDamaged) || l != nil {
			t.Errorf("%s: Open = %v, %v; want ErrDamaged", name, l, err)
			continue
		}
		offset := whole + strings.Index(c.tail, "\n") + 1
		if c.number == 3 {
			offset = whole
		}
		if want := fmt.Sprintf("line %d, at byte %d", c.number, offset); !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %q does not name %q", name, err, want)
		}
		if strings.Contains(err.Error(), "planted") || strings.Contains(err.Error(), "ppp") {
			t.Errorf("%s: %q carries the line's bytes", name, err)
		}
		if got := len(contents(t, filepath.Join(dir, FileName))); got != whole+len(c.tail) {
			t.Errorf("%s: the refused Open changed the file to %d bytes", name, got)
		}
	}
}

func TestOpenRefusesATailNoWriteLeaves(t *testing.T) {
	for _, tail := range []string{"x", `{"eventId":"e`, `{"findingRecord":{}}x`, `{"findingRecord":{}}`, "{}", `{"findingRecord":{}}{"findingRecord":`} {
		dir, whole := logWithTail(t, tail)
		if l, err := Open(dir); !errors.Is(err, ErrDamaged) || l != nil {
			t.Errorf("tail %q: Open = %v, %v; want ErrDamaged", tail, l, err)
		}
		if got := len(contents(t, filepath.Join(dir, FileName))); got != whole+len(tail) {
			t.Errorf("tail %q: the refused Open changed the file to %d bytes", tail, got)
		}
	}
}

// TestATailLongerThanAnyLineIsDamaged: the tail is the start of a line in
// both cases; only its length tells them apart.
func TestATailLongerThanAnyLineIsDamaged(t *testing.T) {
	head := `{"findingRecord":{"schemaVersion":"`
	for _, c := range []struct {
		size int
		want error
	}{{MaxLineBytes, nil}, {MaxLineBytes + 1, ErrDamaged}} {
		dir := logDir(t)
		writeFile(t, filepath.Join(dir, FileName), head+strings.Repeat("a", c.size-len(head)))
		l, err := Open(dir)
		if !errors.Is(err, c.want) || (err == nil) != (c.want == nil) {
			t.Errorf("a tail of %d bytes: Open = %v, want %v", c.size, err, c.want)
		}
		if l != nil {
			mustDo(t, l.Close())
		}
	}
}
