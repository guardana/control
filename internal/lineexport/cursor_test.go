package lineexport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestACursorParsesInItsOneSpelling: every other spelling of a cursor is the
// format's malformed refusal.
func TestACursorParsesInItsOneSpelling(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("0", 63)+"1"
	good := "v1:" + a + ":42:" + b
	c, err := parseCursor(good, testFormat().Refusals)
	if err != nil || c.Offset != 42 || c.First[0] != 0xaa || c.Line[31] != 0x01 || c.String() != good {
		t.Fatalf("parseCursor(%q) = %+v, %v", good, c, err)
	}
	for name, s := range map[string]string{
		"empty":         "",
		"another major": "v2:" + a + ":42:" + b,
		"capitals":      "v1:" + strings.ToUpper(a) + ":42:" + b,
		"short digest":  "v1:" + a[1:] + ":42:" + b,
		"leading zero":  "v1:" + a + ":042:" + b,
		"zero":          "v1:" + a + ":0:" + b,
		"sign":          "v1:" + a + ":+42:" + b,
		"overflow":      "v1:" + a + ":99999999999999999999:" + b,
		"five parts":    good + ":1",
	} {
		if _, err := parseCursor(s, testFormat().Refusals); !errors.Is(err, errMalformed) {
			t.Errorf("%s: parseCursor(%q) = %v, want the format's malformed refusal", name, s, err)
		}
	}
}

// FuzzParseCursor: a cursor parses only in its one spelling, and anything
// else is the format's malformed refusal.
func FuzzParseCursor(f *testing.F) {
	f.Add("v1:" + strings.Repeat("a", 64) + ":42:" + strings.Repeat("0", 64))
	f.Add("v1:" + strings.Repeat("A", 64) + ":042:" + strings.Repeat("0", 64))
	f.Add("v1::1:")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := parseCursor(s, testFormat().Refusals)
		switch {
		case err == nil && c.String() != s:
			t.Fatalf("%q parses and spells %q", s, c.String())
		case err != nil && !errors.Is(err, errMalformed):
			t.Fatalf("%q: %v, want the format's malformed refusal", s, err)
		}
	})
}

// TestACursorHoldsOnlyWhereItWasTaken: a cursor resumes after its line in its
// own file, and each way it can miss is the format's refusal, with nothing
// written.
func TestACursorHoldsOnlyWhereItWasTaken(t *testing.T) {
	lines := []string{`"n a 1"`, `"n b 1"`, `"n c 1"`}
	file := body(lines...)
	q := query(10)
	for n := 1; n <= 3; n++ {
		q.After = cursorAt(lines, n)
		got, tr := export(t, src(file, false), q)
		if tr.Counts["note"] != 3-n || !tr.EndReached || tr.NextCursor != cursorAt(lines, 3) ||
			strings.Contains(got, fmt.Sprintf(`"offset":%d,`, 8*(n-1))) {
			t.Errorf("resumed after line %d: trailer %+v\n%s", n, tr, got)
		}
	}

	other := []string{`"n z 1"`, `"n b 1"`, `"n c 1"`}
	changed := []string{`"n a 1"`, `"n y 1"`, `"n c 1"`}
	for name, c := range map[string]struct {
		file, after string
		want        error
	}{
		"another file":       {body(other...), cursorAt(lines, 2), errOther},
		"an empty file":      {"", cursorAt(lines, 1), errOther},
		"past the end":       {body(lines[:2]...), cursorAt(lines, 3), errPastEnd},
		"off a line":         {file, strings.Replace(cursorAt(lines, 2), ":16:", ":15:", 1), errOffLine},
		"a line changed":     {body(changed...), cursorAt(lines, 2), errChanged},
		"another spelling":   {file, "v1:x", errMalformed},
		"a file cut under":   {file, cursorAt(lines, 2), errShort},
		"no whole first one": {`"n a`, cursorAt(lines, 1), errOther},
	} {
		s := src(c.file, false)
		if name == "a file cut under" {
			s.R = strings.NewReader(c.file[:10])
		}
		q.After = c.after
		var out bytes.Buffer
		if _, err := Export(testFormat(), s, q, judge, &out); !errors.Is(err, c.want) || out.Len() != 0 {
			t.Errorf("%s: Export = %v with %q written, want %v and nothing written", name, err, out.String(), c.want)
		}
	}
}

// TestALineAtTheFormatsBoundIsHeld: a line of MaxLineBytes before its newline
// is judged, and one byte more is a gap.
func TestALineAtTheFormatsBoundIsHeld(t *testing.T) {
	at := `"n e ` + strings.Repeat("e", 34) + `"`
	over := `"n f ` + strings.Repeat("f", 35) + `"`
	got, tr := export(t, src(body(at, over), false), query(10))
	if len(at) != 40 || tr.Counts["note"] != 1 || tr.Counts[Gap] != 1 || !strings.Contains(got, `"reason":"too_long"`) {
		t.Errorf("trailer %+v\n%s", tr, got)
	}
}

// TestAnIncompleteFormatOrQueryIsRefused: an export that could not be written
// as one JSON object per record, or wraps no error of the format's, writes
// nothing.
func TestAnIncompleteFormatOrQueryIsRefused(t *testing.T) {
	cases := map[string]func(*call){
		"no name":             func(c *call) { c.f.Name = "" },
		"no version":          func(c *call) { c.f.Version = "" },
		"no conflict reason":  func(c *call) { c.f.Conflict = "" },
		"no line bound":       func(c *call) { c.f.MaxLineBytes = 0 },
		"no line type":        func(c *call) { c.f.Lines = nil },
		"a reserved type":     func(c *call) { c.f.Lines = []string{"note", Gap} },
		"a member type":       func(c *call) { c.f.Lines = []string{"note", "cursor"} },
		"a quoted type":       func(c *call) { c.f.Lines = []string{"note", `re"port`} },
		"an id member taken":  func(c *call) { c.f.IDMember = "first_offset" },
		"no id member":        func(c *call) { c.f.IDMember = "" },
		"no malformed":        func(c *call) { c.f.Refusals.CursorMalformed = nil },
		"no other file":       func(c *call) { c.f.Refusals.CursorOtherFile = nil },
		"no past end":         func(c *call) { c.f.Refusals.CursorPastEnd = nil },
		"no off line":         func(c *call) { c.f.Refusals.CursorOffLine = nil },
		"no changed":          func(c *call) { c.f.Refusals.CursorChanged = nil },
		"no short read":       func(c *call) { c.f.Refusals.ShortRead = nil },
		"no byte bound":       func(c *call) { c.f.Refusals.ByteBound = nil },
		"no judge":            func(c *call) { c.j = nil },
		"no reader":           func(c *call) { c.s.R = nil },
		"no writer":           func(c *call) { c.w = nil },
		"no echo":             func(c *call) { c.q.Echo = nil },
		"a negative size":     func(c *call) { c.s.Size = -1 },
		"a limit of zero":     func(c *call) { c.q.Limit = 0 },
		"a negative bound":    func(c *call) { c.q.MaxBytes = -1 },
		"none, the control":   func(*call) {},
		"two types, the same": func(c *call) { c.f.Lines = []string{"note", "report", "note"} },
	}
	for name, change := range cases {
		var out bytes.Buffer
		c := call{testFormat(), src(body(`"n a 1"`), false), query(10), judge, &out}
		reads := 0
		c.s.R = countReads{c.s.R, &reads}
		change(&c)
		_, err := Export(c.f, c.s, c.q, c.j, c.w)
		if name == "none, the control" {
			if err != nil || out.Len() == 0 || reads == 0 {
				t.Errorf("the control: Export = %v after %d reads", err, reads)
			}
			continue
		}
		if !errors.Is(err, ErrInvalid) || out.Len() != 0 || reads != 0 {
			t.Errorf("%s: Export = %v with %q written after %d reads, want ErrInvalid before a read", name, err, out.String(), reads)
		}
	}
}

// call is the arguments of one Export.
type call struct {
	f Format
	s Source
	q Query
	j Judge
	w io.Writer
}

// countReads counts the reads of a source, so a refusal is seen to come
// before the file is read.
type countReads struct {
	r io.ReaderAt
	n *int
}

func (c countReads) ReadAt(p []byte, off int64) (int, error) {
	*c.n++
	return c.r.ReadAt(p, off)
}
