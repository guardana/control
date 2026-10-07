package lineexport

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// committedAt is src with its committed end at n bytes.
func committedAt(s Source, n int64) Source {
	s.Committed = func() (int64, error) { return n, nil }
	return s
}

// TestNothingPastTheCommittedEndIsRead: whole lines after the committed end
// are a write still open; they are not exported, count as tail bytes with the
// bytes after the last newline, and are a gap only when no writer holds the
// file.
func TestNothingPastTheCommittedEndIsRead(t *testing.T) {
	lines := []string{`"n a 1"`, `"n b 1"`, `"n c 1"`}
	file := body(lines...) + `"n d`
	for _, held := range []bool{false, true} {
		got, tr := export(t, committedAt(src(file, held), 8), query(10))
		if strings.Contains(got, `"n b 1"`) || strings.Contains(got, `"n c 1"`) || tr.Counts["note"] != 1 {
			t.Errorf("held %v: a line past the committed end was exported\n%s", held, got)
		}
		if tr.TailBytes != int64(len(file)-8) || tr.NextCursor != cursorAt(lines, 1) || !tr.EndReached || tr.ScannedBytes != 8 {
			t.Errorf("held %v: trailer %+v", held, tr)
		}
		gap := `{"type":"gap","offset":8,"reason":"partial_tail"}`
		if strings.Contains(got, gap) == held || tr.Counts[Gap] != map[bool]int{false: 1, true: 0}[held] {
			t.Errorf("held %v: the open write's gap written %v\n%s", held, strings.Contains(got, gap), got)
		}
	}
}

// TestACursorPastTheCommittedEndIsRefused: a cursor at the end of a line the
// file holds but no write committed names nothing an export returned.
func TestACursorPastTheCommittedEndIsRefused(t *testing.T) {
	lines := []string{`"n a 1"`, `"n b 1"`}
	q := query(10)
	q.After = cursorAt(lines, 2)
	if _, err := Export(testFormat(), committedAt(src(body(lines...), false), 8), q, judge, &bytes.Buffer{}); !errors.Is(err, errPastEnd) {
		t.Errorf("Export = %v, want the format's past-the-end refusal", err)
	}
	q.After = cursorAt(lines, 1)
	if got, _ := export(t, committedAt(src(body(lines...), false), 8), q); strings.Contains(got, `"n b 1"`) {
		t.Errorf("a cursor at the committed end exported the open write\n%s", got)
	}
}

// TestNoCommittedLineGivesNoIdentity: a file whose committed end is its first
// byte has no first line to name it by, so it gives no cursor and no source.
func TestNoCommittedLineGivesNoIdentity(t *testing.T) {
	got, tr := export(t, committedAt(src(body(`"n a 1"`), false), 0), query(10))
	if strings.Contains(got, `"source"`) || tr.NextCursor != "" || tr.TailBytes != 8 || tr.Counts["note"] != 0 {
		t.Errorf("trailer %+v\n%s", tr, got)
	}
}

// TestACommittedEndOffALineOrPastTheLastNewlineIsRefused: the committed end
// is where a line ends, inside what the file holds, or the export has no
// line to start the tail at.
func TestACommittedEndOffALineOrPastTheLastNewlineIsRefused(t *testing.T) {
	file := body(`"n a 1"`, `"n b 1"`) + "x"
	for _, end := range []int64{-1, 1, 7, 9, 17, 18} {
		var out bytes.Buffer
		if _, err := Export(testFormat(), committedAt(src(file, false), end), query(10), judge, &out); !errors.Is(err, ErrInvalid) || out.Len() != 0 {
			t.Errorf("committed end %d: Export = %v with %q written, want ErrInvalid and nothing", end, err, out.String())
		}
	}
	for _, end := range []int64{0, 8, 16} {
		if _, err := Export(testFormat(), committedAt(src(file, false), end), query(10), judge, &bytes.Buffer{}); err != nil {
			t.Errorf("committed end %d: Export = %v", end, err)
		}
	}
	s := src(file, false)
	s.Committed = func() (int64, error) { return 0, errors.New("scan failed") }
	var out bytes.Buffer
	if _, err := Export(testFormat(), s, query(10), judge, &out); err == nil || out.Len() != 0 {
		t.Errorf("a failed committed end: Export = %v with %q written", err, out.String())
	}
}

// TestTheTrailerSaysTheIdentityItWasGiven: a source that says what its
// identity rests on has the trailer repeat it; one that does not writes no
// such member, as the formats written before it do.
func TestTheTrailerSaysTheIdentityItWasGiven(t *testing.T) {
	s := src(body(`"n a 1"`), false)
	got, _ := export(t, s, query(10))
	if strings.Contains(got, `"identity"`) {
		t.Errorf("a source with no identity wrote one\n%s", got)
	}
	s.Identity = "log_id"
	got, _ = export(t, s, query(10))
	if !strings.HasSuffix(got, `"dedup_scope":"export","identity":"log_id"}`+"\n") {
		t.Errorf("the trailer does not end with the identity\n%s", got)
	}
}

// TestALineWithNoRecordIsConsumedAndWritesNothing: a verdict that passes a
// line by with no type writes no record, takes none of the limit and moves
// the cursor past the line; one that also names an id is refused.
func TestALineWithNoRecordIsConsumedAndWritesNothing(t *testing.T) {
	lines := []string{`"h"`, `"n a 1"`, `"n b 1"`}
	skip := func(l Line) Verdict {
		switch string(l.Body()) {
		case `"h"`:
			return Verdict{Pass: true}
		case `"i"`:
			return Verdict{Pass: true, ID: "i"}
		}
		return judge(l)
	}
	var out bytes.Buffer
	tr, err := Export(testFormat(), src(body(lines...), false), query(1), skip, &out)
	if err != nil {
		t.Fatalf("Export = %v", err)
	}
	if strings.Contains(out.String(), `"h"`) || tr.Counts["note"] != 1 || tr.NextCursor != cursorAt(lines, 2) {
		t.Errorf("trailer %+v\n%s", tr, out.String())
	}
	out.Reset()
	if _, err := Export(testFormat(), src(body(`"i"`), false), query(1), skip, &out); !errors.Is(err, ErrInvalid) {
		t.Errorf("a line with no type and an id: Export = %v, want ErrInvalid", err)
	}
}
