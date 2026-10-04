package lineexport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The test format's refusals: errors.Is against these proves the export wraps
// the format's own errors, not errors of its own.
var (
	errMalformed = errors.New("test: malformed")
	errOther     = errors.New("test: other file")
	errPastEnd   = errors.New("test: past end")
	errOffLine   = errors.New("test: off line")
	errChanged   = errors.New("test: changed")
	errShort     = errors.New("test: short read")
	errBound     = errors.New("test: byte bound")
)

func testFormat() Format {
	return Format{Name: "test.line-export", Version: "0.1", Lines: []string{"note", "report"}, IDMember: "note_id",
		Conflict: "conflicting_id", MaxLineBytes: 40, Refusals: Refusals{
			CursorMalformed: errMalformed, CursorOtherFile: errOther, CursorPastEnd: errPastEnd, CursorOffLine: errOffLine,
			CursorChanged: errChanged, ShortRead: errShort, ByteBound: errBound,
		}}
}

// judge reads a line spelled as a JSON string "<code> <id> <content>": n a
// note, r a report, p a note the filters pass by, g a gap, x a type the
// format does not name, e a gap with no reason.
func judge(l Line) Verdict {
	s, err := strconv.Unquote(string(l.Body()))
	if err != nil {
		return Verdict{Type: Gap, Reason: "unquoted"}
	}
	f := append(strings.Fields(s), "", "", "")
	content := sha256.Sum256([]byte(f[2]))
	switch f[0] {
	case "n":
		return Verdict{Type: "note", ID: f[1], Content: content}
	case "r":
		return Verdict{Type: "report", ID: f[1], Content: content}
	case "p":
		return Verdict{Type: "note", Pass: true, ID: f[1], Content: content}
	case "g":
		return Verdict{Type: Gap, Reason: "bad"}
	case "x":
		return Verdict{Type: "weird", ID: f[1], Content: content}
	case "e":
		return Verdict{Type: Gap}
	}
	return Verdict{}
}

func body(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func src(body string, held bool) Source {
	return Source{Name: "f", R: strings.NewReader(body), Size: int64(len(body)), Held: func() (bool, error) { return held, nil }}
}

func query(limit int) Query { return Query{Limit: limit, Echo: map[string]int{"limit": limit}} }

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// cursorAt spells the cursor after the n-th line of lines, computed apart
// from the export.
func cursorAt(lines []string, n int) string {
	offset := 0
	for _, l := range lines[:n] {
		offset += len(l) + 1
	}
	return fmt.Sprintf("v1:%s:%d:%s", sum(lines[0]+"\n"), offset, sum(lines[n-1]+"\n"))
}

func export(t *testing.T, s Source, q Query) (string, Trailer) {
	t.Helper()
	var out bytes.Buffer
	tr, err := Export(testFormat(), s, q, judge, &out)
	if err != nil {
		t.Fatalf("Export = %v", err)
	}
	return out.String(), tr
}

// TestEachVerdictWritesItsRecord: a record of every type, with the format's
// names, a conflict and a duplicate found by content rather than by the line's
// bytes, a line passed by and a line too long to hold.
func TestEachVerdictWritesItsRecord(t *testing.T) {
	long := `"n z ` + strings.Repeat("z", 36) + `"`
	lines := []string{`"n a 1"`, `"r b 1"`, `"g"`, `"p c 1"`, `"n a 2"`, `"r b 1 x"`, long, `"n d 1"`}
	got, tr := export(t, src(body(lines...), false), query(10))
	c := func(n int) string { return cursorAt(lines, n) }
	want := `{"type":"header","format":"test.line-export","version":"0.1","file":"f","source":"` + sum(lines[0]+"\n") + `","query":{"limit":10}}` + "\n" +
		`{"type":"note","offset":0,"cursor":"` + c(1) + `","note":"n a 1"}` + "\n" +
		`{"type":"report","offset":8,"cursor":"` + c(2) + `","report":"r b 1"}` + "\n" +
		`{"type":"gap","offset":16,"cursor":"` + c(3) + `","reason":"bad"}` + "\n" +
		`{"type":"gap","offset":28,"cursor":"` + c(5) + `","reason":"conflicting_id"}` + "\n" +
		`{"type":"duplicate","offset":36,"note_id":"b","first_offset":8}` + "\n" +
		`{"type":"gap","offset":46,"cursor":"` + c(7) + `","reason":"too_long"}` + "\n" +
		`{"type":"note","offset":89,"cursor":"` + c(8) + `","note":"n d 1"}` + "\n" +
		`{"type":"trailer","next_cursor":"` + c(8) + `","end_reached":true,"tail_bytes":0,"writer_held":false,` +
		`"counts":{"note":2,"report":1,"gap":3,"duplicate":1},"scanned_bytes":97,"dedup_scope":"export"}` + "\n"
	if got != want {
		t.Errorf("export\n%s\nwant\n%s", got, want)
	}
	if tr.NextCursor != c(8) || !tr.EndReached || tr.ScannedBytes != 97 ||
		tr.Counts["note"] != 2 || tr.Counts["report"] != 1 || tr.Counts[Gap] != 3 || tr.Counts[Duplicate] != 1 {
		t.Errorf("trailer %+v", tr)
	}
}

// TestAVerdictOutsideTheFormatIsRefused: a type the format does not name, and
// a gap with no reason, stop the export without its trailer.
func TestAVerdictOutsideTheFormatIsRefused(t *testing.T) {
	for _, line := range []string{`"x a 1"`, `"e"`} {
		var out bytes.Buffer
		_, err := Export(testFormat(), src(body(`"n a 1"`, line), false), query(10), judge, &out)
		if !errors.Is(err, ErrInvalid) || strings.Contains(out.String(), "trailer") {
			t.Errorf("%s: Export = %v with %q written, want ErrInvalid and no trailer", line, err, out.String())
		}
	}
}

// TestTheLimitCountsRecordsNotLinesPassedBy: a line passed by is consumed at
// the limit, and the export stops before the first line that would write a
// record past it.
func TestTheLimitCountsRecordsNotLinesPassedBy(t *testing.T) {
	lines := []string{`"n a 1"`, `"p b 1"`, `"g"`}
	_, tr := export(t, src(body(lines...), false), query(1))
	if tr.NextCursor != cursorAt(lines, 2) || tr.EndReached || tr.Counts["note"] != 1 || tr.Counts[Gap] != 0 {
		t.Errorf("limit 1: trailer %+v, want the line passed by consumed and the gap not", tr)
	}
	_, tr = export(t, src(body(lines...), false), query(2))
	if tr.NextCursor != cursorAt(lines, 3) || !tr.EndReached || tr.Counts[Gap] != 1 {
		t.Errorf("limit 2: trailer %+v", tr)
	}
}

// TestTheByteBoundStopsBeforeALine: the bound counts whole lines, stops
// before the line that would pass it, and refuses a first line longer than
// it, which no export under that bound could pass.
func TestTheByteBoundStopsBeforeALine(t *testing.T) {
	lines := []string{`"n a 1"`, `"n b 1"`, `"n c 1"`}
	for _, c := range []struct {
		bound int64
		read  int
	}{{0, 3}, {24, 3}, {23, 2}, {16, 2}, {15, 1}, {8, 1}} {
		q := query(10)
		q.MaxBytes = c.bound
		_, tr := export(t, src(body(lines...), false), q)
		if tr.Counts["note"] != c.read || tr.ScannedBytes != int64(8*c.read) || tr.NextCursor != cursorAt(lines, c.read) ||
			tr.EndReached != (c.read == 3) {
			t.Errorf("bound %d: trailer %+v, want %d lines read", c.bound, tr, c.read)
		}
	}
	q := query(10)
	q.MaxBytes = 7
	if _, err := Export(testFormat(), src(body(lines...), false), q, judge, &bytes.Buffer{}); !errors.Is(err, errBound) {
		t.Errorf("bound 7 under an 8-byte line: Export = %v, want the format's byte-bound refusal", err)
	}
}

// TestATailIsAGapOnlyWithNoWriterAndRoom: bytes after the last newline are a
// gap when no writer holds the file and the limit leaves room, and the export
// has not reached the end when it does not.
func TestATailIsAGapOnlyWithNoWriterAndRoom(t *testing.T) {
	lines := []string{`"n a 1"`, `"n b 1"`}
	file := body(lines...) + `"n c`
	next := cursorAt(lines, 2)
	for name, c := range map[string]struct {
		held  bool
		limit int
		want  Trailer
	}{
		"no writer": {false, 10, Trailer{NextCursor: next, EndReached: true, TailBytes: 4, ScannedBytes: 16,
			Counts: map[string]int{"note": 2, "report": 0, Gap: 1, Duplicate: 0}}},
		"held": {true, 10, Trailer{NextCursor: next, EndReached: true, TailBytes: 4, WriterHeld: true, ScannedBytes: 16,
			Counts: map[string]int{"note": 2, "report": 0, Gap: 0, Duplicate: 0}}},
		"no room": {false, 2, Trailer{NextCursor: next, TailBytes: 4, ScannedBytes: 16,
			Counts: map[string]int{"note": 2, "report": 0, Gap: 0, Duplicate: 0}}},
	} {
		got, tr := export(t, src(file, c.held), query(c.limit))
		if !reflect.DeepEqual(tr, c.want) {
			t.Errorf("%s: trailer %+v, want %+v", name, tr, c.want)
		}
		if gap := `{"type":"gap","offset":16,"reason":"partial_tail"}`; strings.Contains(got, gap) != (c.want.Counts[Gap] == 1) {
			t.Errorf("%s: the tail's gap written %v\n%s", name, strings.Contains(got, gap), got)
		}
	}
}

// TestAWriterIsAskedOnlyAboutATail: a file ending in a newline asks nothing,
// and a question that fails refuses the export with nothing written.
func TestAWriterIsAskedOnlyAboutATail(t *testing.T) {
	file := body(`"n a 1"`)
	s := src(file, false)
	s.Held = func() (bool, error) { t.Error("a file ending in a newline asked about a writer"); return false, nil }
	export(t, s, query(10))
	s = src(file+"x", false)
	s.Held = func() (bool, error) { return false, errors.New("probe failed") }
	var out bytes.Buffer
	if _, err := Export(testFormat(), s, query(10), judge, &out); err == nil || out.Len() != 0 {
		t.Errorf("a failed probe: Export = %v with %q written", err, out.String())
	}
}

// recordsOf exports lines and returns the records between the header and the
// trailer.
func recordsOf(t *testing.T, lines ...string) []string {
	t.Helper()
	got, _ := export(t, src(body(lines...), false), query(10))
	out := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	return out[1 : len(out)-1]
}

// TestAnIDIsKeptPerTypeWrittenOrPassedBy: a duplicate names a line the export
// wrote, a line passed by still holds its id against a conflict, a line the
// filters pass by is passed by though a written one repeats it, each type keeps
// its own ids, and a duplicate's id is escaped as JSON.
func TestAnIDIsKeptPerTypeWrittenOrPassedBy(t *testing.T) {
	quoted := `"n a\"b< 1"`
	for name, c := range map[string]struct {
		lines []string
		want  func(c func(int) string) []string
	}{
		"of a line written, not one passed by": {[]string{`"p a 1"`, `"n a 1"`, `"n a 1"`}, func(c func(int) string) []string {
			return []string{`{"type":"note","offset":8,"cursor":"` + c(2) + `","note":"n a 1"}`,
				`{"type":"duplicate","offset":16,"note_id":"a","first_offset":8}`}
		}},
		"a repeat passed by": {[]string{`"n a 1"`, `"p a 1"`, `"n a 1"`}, func(c func(int) string) []string {
			return []string{`{"type":"note","offset":0,"cursor":"` + c(1) + `","note":"n a 1"}`,
				`{"type":"duplicate","offset":16,"note_id":"a","first_offset":0}`}
		}},
		"a conflict with a line passed by": {[]string{`"p a 1"`, `"n a 2"`}, func(c func(int) string) []string {
			return []string{`{"type":"gap","offset":8,"cursor":"` + c(2) + `","reason":"conflicting_id"}`}
		}},
		"one id, two types": {[]string{`"n a 1"`, `"r a 1"`, `"r a 1"`, `"n a 1"`, `"r a 2"`}, func(c func(int) string) []string {
			return []string{`{"type":"note","offset":0,"cursor":"` + c(1) + `","note":"n a 1"}`,
				`{"type":"report","offset":8,"cursor":"` + c(2) + `","report":"r a 1"}`,
				`{"type":"duplicate","offset":16,"note_id":"a","first_offset":8}`,
				`{"type":"duplicate","offset":24,"note_id":"a","first_offset":0}`,
				`{"type":"gap","offset":32,"cursor":"` + c(5) + `","reason":"conflicting_id"}`}
		}},
		"an id JSON escapes": {[]string{quoted, quoted}, func(c func(int) string) []string {
			return []string{`{"type":"note","offset":0,"cursor":"` + c(1) + `","note":` + quoted + `}`,
				`{"type":"duplicate","offset":12,"note_id":"a\"b\u003c","first_offset":0}`}
		}},
	} {
		got := recordsOf(t, c.lines...)
		if want := c.want(func(n int) string { return cursorAt(c.lines, n) }); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n%s\nwant\n%s", name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}
