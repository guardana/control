package trailfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/evidence"
)

// Lines spelled out, so no expected value comes from the encoder or the
// export under test.
const (
	lineE1 = `{"eventId":"e1","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r1","runId":"run1","projectId":"p1","tenantId":"t1","schemaVersion":"1.0"}`
	lineE2 = `{"eventId":"e2","kind":"EVENT_KIND_POLICY_DECIDED","requestId":"r1","runId":"run1","projectId":"p1","tenantId":"t1","schemaVersion":"1.0","prevEventId":"e1"}`
	lineE3 = `{"eventId":"e3","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r2","runId":"run2","projectId":"p2","tenantId":"t2","schemaVersion":"1.0"}`
	// lineMajor2 is an event of a major this build does not read.
	lineMajor2 = `{"eventId":"e4","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r1","projectId":"p1","tenantId":"t1","schemaVersion":"2.0"}`
	// lineUnknown carries a field the contract does not name.
	lineUnknown = `{"eventId":"e5","schemaVersion":"1.0","later":true}`
	// lineE1Other is e1's id with other content.
	lineE1Other = `{"eventId":"e1","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r1","runId":"other","projectId":"p1","tenantId":"t1","schemaVersion":"1.0"}`
	partialTail = `{"eventId":"e6","kin`
)

// record is one export line, every member any type has.
type record struct {
	Type        string          `json:"type"`
	Offset      *int64          `json:"offset"`
	Cursor      string          `json:"cursor"`
	Reason      string          `json:"reason"`
	EventID     string          `json:"event_id"`
	FirstOffset *int64          `json:"first_offset"`
	Event       json.RawMessage `json:"event"`
	Format      string          `json:"format"`
	Version     string          `json:"version"`
	File        string          `json:"file"`
	Source      string          `json:"source"`
	Query       json.RawMessage `json:"query"`
	NextCursor  string          `json:"next_cursor"`
	EndReached  *bool           `json:"end_reached"`
	TailBytes   *int64          `json:"tail_bytes"`
	WriterHeld  *bool           `json:"writer_held"`
	Counts      map[string]int  `json:"counts"`
	Scanned     *int64          `json:"scanned_bytes"`
	DedupScope  string          `json:"dedup_scope"`
}

// short is a record as the tests spell what they expect: the type, where it
// starts and what distinguishes it.
func (r record) short() string {
	switch r.Type {
	case "event":
		return fmt.Sprintf("event@%d", *r.Offset)
	case "gap":
		return fmt.Sprintf("gap@%d:%s", *r.Offset, r.Reason)
	case "duplicate":
		return fmt.Sprintf("duplicate@%d:%s@%d", *r.Offset, r.EventID, *r.FirstOffset)
	default:
		return r.Type
	}
}

type exported struct {
	raw     string
	header  record
	body    []record
	trailer record
	result  Trailer
}

func (e exported) shorts() []string {
	out := make([]string, 0, len(e.body))
	for _, r := range e.body {
		out = append(out, r.short())
	}
	return out
}

func source(body string, held bool) Source {
	return Source{Name: "trail.jsonl", R: strings.NewReader(body), Size: int64(len(body)),
		Held: func() (bool, error) { return held, nil }}
}

func query() Query { return Query{Limit: DefaultExportLimit} }

// runExport exports body and parses what it wrote, failing on a refusal and on
// output that is not framed: one header first, one trailer last, one JSON
// object per line.
func runExport(t *testing.T, body string, held bool, q Query) exported {
	t.Helper()
	var out bytes.Buffer
	tr, err := Export(source(body, held), q, &out)
	if err != nil {
		t.Fatalf("Export = %v", err)
	}
	e := exported{raw: out.String(), result: tr}
	e.header, e.body, e.trailer = frame(t, out.String())
	return e
}

// frame splits an export into its header, its records and its trailer, and
// fails on a line that is not one JSON object of a known type in its place.
func frame(t *testing.T, out string) (header record, body []record, trailer record) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if !strings.HasSuffix(out, "\n") || len(lines) < 2 {
		t.Fatalf("export is not framed:\n%s", out)
	}
	for i, line := range lines {
		var r record
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("line %d %q: %v", i+1, line, err)
		}
		if !placed(i, len(lines), r.Type) {
			t.Fatalf("line %d is a %q record out of place:\n%s", i+1, r.Type, out)
		}
		switch r.Type {
		case "header":
			header = r
		case "trailer":
			trailer = r
		default:
			body = append(body, r)
		}
	}
	return header, body, trailer
}

// placed is a record of type typ at line i of n where the frame allows it.
func placed(i, n int, typ string) bool {
	switch typ {
	case "header":
		return i == 0
	case "trailer":
		return i == n-1
	case "event", "gap", "duplicate":
		return i > 0 && i < n-1
	}
	return false
}

func file(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// cursorAfter spells the cursor after the n-th line of body, computed here
// from the bytes rather than by the export.
func cursorAfter(body string, n int) string {
	lines := strings.SplitAfter(body, "\n")
	first := sha256.Sum256([]byte(lines[0]))
	offset := 0
	for _, l := range lines[:n] {
		offset += len(l)
	}
	line := sha256.Sum256([]byte(lines[n-1]))
	return "v1:" + hex.EncodeToString(first[:]) + ":" + strconv.Itoa(offset) + ":" + hex.EncodeToString(line[:])
}

func offsetOf(body string, n int) int {
	return len(strings.Join(strings.SplitAfter(body, "\n")[:n-1], ""))
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

// TestEachLineBecomesItsRecord: an event of major 2, an unknown field, one id
// with other content, an exact repeat and a blank line in one file, each the
// record its line is, where it stands.
func TestEachLineBecomesItsRecord(t *testing.T) {
	body := file(lineE1, lineMajor2, lineUnknown, lineE1Other, lineE2, lineE1, "", lineE3)
	e := runExport(t, body, false, query())
	equal(t, "records", e.shorts(), []string{
		"event@0",
		fmt.Sprintf("gap@%d:unsupported_version", offsetOf(body, 2)),
		fmt.Sprintf("gap@%d:malformed", offsetOf(body, 3)),
		fmt.Sprintf("gap@%d:conflicting_event_id", offsetOf(body, 4)),
		fmt.Sprintf("event@%d", offsetOf(body, 5)),
		fmt.Sprintf("duplicate@%d:e1@0", offsetOf(body, 6)),
		fmt.Sprintf("gap@%d:malformed", offsetOf(body, 7)),
		fmt.Sprintf("event@%d", offsetOf(body, 8)),
	})
	for i, r := range e.body {
		if r.Type != "duplicate" && r.Cursor != cursorAfter(body, i+1) {
			t.Errorf("record %d: cursor %q, want %q", i, r.Cursor, cursorAfter(body, i+1))
		}
	}
	if got := string(e.body[0].Event); got != lineE1 {
		t.Errorf("the event is %s, want the line's bytes %s", got, lineE1)
	}
}

// TestTheHeaderAndTrailerSayWhatWasRead: the header names the format, the
// file and its first line's digest; the trailer where the export ended and
// what it wrote, as Export returns it.
func TestTheHeaderAndTrailerSayWhatWasRead(t *testing.T) {
	body := file(lineE1, lineMajor2, lineUnknown, lineE1Other, lineE2, lineE1, "", lineE3)
	e := runExport(t, body, false, query())
	want := Trailer{NextCursor: cursorAfter(body, 8), EndReached: true, Events: 3, Gaps: 4, Duplicates: 1, ScannedBytes: int64(len(body))}
	if e.result != want {
		t.Errorf("trailer %+v, want %+v", e.result, want)
	}
	if e.trailer.NextCursor != want.NextCursor || e.trailer.Counts["gap"] != 4 || e.trailer.Counts["event"] != 3 ||
		e.trailer.Counts["duplicate"] != 1 || e.trailer.DedupScope != "export" || !*e.trailer.EndReached {
		t.Errorf("trailer record %+v", e.trailer)
	}
	first := sha256.Sum256([]byte(lineE1 + "\n"))
	if e.header.Format != brand.OTelNamespace+".evidence-export" || e.header.Version != "1.0" ||
		e.header.File != "trail.jsonl" || e.header.Source != hex.EncodeToString(first[:]) {
		t.Errorf("header %+v", e.header)
	}
}

// TestAPartialTailIsAGapOnlyWithNoWriter: bytes after the last newline are a
// line still being written while a writer holds the file, and a gap when none
// does; either way the trailer counts them and the next export starts before
// them.
func TestAPartialTailIsAGapOnlyWithNoWriter(t *testing.T) {
	body := file(lineE1, lineE2) + partialTail
	whole := int64(len(file(lineE1, lineE2)))
	held := runExport(t, body, true, query())
	equal(t, "held", held.shorts(), []string{"event@0", fmt.Sprintf("event@%d", len(lineE1)+1)})
	want := Trailer{NextCursor: cursorAfter(body, 2), EndReached: true, TailBytes: int64(len(partialTail)), WriterHeld: true,
		Events: 2, ScannedBytes: whole}
	if held.result != want {
		t.Errorf("held: trailer %+v, want %+v", held.result, want)
	}
	loose := runExport(t, body, false, query())
	equal(t, "not held", loose.shorts(), []string{"event@0", fmt.Sprintf("event@%d", len(lineE1)+1), fmt.Sprintf("gap@%d:partial_tail", whole)})
	want.WriterHeld, want.Gaps = false, 1
	if loose.result != want {
		t.Errorf("not held: trailer %+v, want %+v", loose.result, want)
	}
	if tail := loose.body[2]; tail.Cursor != "" {
		t.Errorf("the tail's gap carries cursor %q; no newline ends it", tail.Cursor)
	}
	var asked bool
	src := source(file(lineE1), false)
	src.Held = func() (bool, error) { asked = true; return false, nil }
	if _, err := Export(src, query(), &bytes.Buffer{}); err != nil || asked {
		t.Errorf("a file ending in a newline: Export = %v, writer asked %v; want no question", err, asked)
	}
	src = source(body, false)
	src.Held = func() (bool, error) { return false, errors.New("probe failed") }
	var out bytes.Buffer
	if _, err := Export(src, query(), &out); err == nil || out.Len() != 0 {
		t.Errorf("a failed probe: Export = %v with %q written, want a refusal and nothing written", err, out.String())
	}
	src = source(body, false)
	src.Held = nil
	if tr, err := Export(src, query(), &bytes.Buffer{}); err != nil || tr.Gaps != 1 {
		t.Errorf("no probe: Export = %+v, %v; want the tail a gap", tr, err)
	}
}

// TestAFileWithNoWholeLineGivesNoCursor: an empty file and one holding only
// an unfinished line have no identity, so nothing names a place in them.
func TestAFileWithNoWholeLineGivesNoCursor(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "a partial line": partialTail} {
		e := runExport(t, body, true, query())
		if e.header.Source != "" || e.trailer.NextCursor != "" || e.result.NextCursor != "" || !e.result.EndReached || len(e.body) != 0 {
			t.Errorf("%s: header %+v trailer %+v body %v", name, e.header, e.result, e.shorts())
		}
		if strings.Contains(e.raw, `"next_cursor"`) || strings.Contains(e.raw, `"source"`) {
			t.Errorf("%s: export names a cursor or a source:\n%s", name, e.raw)
		}
		q := query()
		q.After = cursorAfter(file(lineE1), 1)
		if _, err := Export(source(body, true), q, &bytes.Buffer{}); !errors.Is(err, ErrCursorOtherFile) {
			t.Errorf("%s: a cursor = %v, want ErrCursorOtherFile", name, err)
		}
	}
}

// TestTheExportedEventIsTheLinesBytes: a line spelled otherwise than the
// codec writes it, with space, another member order and characters an
// encoder escapes, is carried byte for byte.
func TestTheExportedEventIsTheLinesBytes(t *testing.T) {
	odd := `{ "schemaVersion":"1.0", "eventId":"<e&1>" ,"kind":"EVENT_KIND_ACTION_PROPOSED",  "requestId":"r` + "\u00e9" + `"}`
	e := runExport(t, file(odd), false, query())
	if len(e.body) != 1 || string(e.body[0].Event) != odd || !strings.Contains(e.raw, `"event":`+odd+"}\n") {
		t.Errorf("export:\n%s\nwant the line %s as it stands", e.raw, odd)
	}
}

// TestACarriageReturnNeverReachesTheExport: the one before a line's newline
// is left out of the event, being whitespace and no part of it; one anywhere
// else in a line the codec reads is whitespace between its tokens, which a
// reader splitting on any line ending would cut the record at, so the line
// is a gap.
func TestACarriageReturnNeverReachesTheExport(t *testing.T) {
	crlf := lineE1 + "\r\n"
	e := runExport(t, crlf, false, query())
	if len(e.body) != 1 || !strings.Contains(e.raw, `"event":`+lineE1+"}\n") {
		t.Errorf("a CRLF line exported as:\n%s", e.raw)
	}
	inner := strings.Replace(lineE1, `,"kind"`, ",\r\"kind\"", 1)
	if _, err := evidence.DecodeJSONL(strings.NewReader(inner+"\n"), 1); err != nil {
		t.Fatalf("the codec refuses a carriage return between tokens: %v", err)
	}
	body := file(inner, lineE1+"\r\r", lineE2+"\r", lineE3)
	e = runExport(t, body, false, query())
	equal(t, "records", e.shorts(), []string{
		"gap@0:carriage_return",
		fmt.Sprintf("gap@%d:carriage_return", offsetOf(body, 2)),
		fmt.Sprintf("event@%d", offsetOf(body, 3)),
		fmt.Sprintf("event@%d", offsetOf(body, 4)),
	})
	if strings.Contains(e.raw, "\r") {
		t.Errorf("a carriage return reached the export:\n%q", e.raw)
	}
	if e.body[0].Cursor != cursorAfter(body, 1) || e.body[1].Cursor != cursorAfter(body, 2) {
		t.Errorf("the gaps carry cursors %q and %q", e.body[0].Cursor, e.body[1].Cursor)
	}
}

// TestALineOverTheCodecsBoundIsAGap: a line of evidence.MaxLineBytes is an
// event, and one byte longer is a gap the export reads past.
func TestALineOverTheCodecsBoundIsAGap(t *testing.T) {
	at := lineE1[:len(lineE1)-1] + strings.Repeat(" ", evidence.MaxLineBytes-len(lineE1)) + "}"
	over := at[:len(at)-1] + " }"
	body := file(at, over, lineE2)
	e := runExport(t, body, false, query())
	equal(t, "records", e.shorts(), []string{"event@0", fmt.Sprintf("gap@%d:too_long", len(at)+1),
		fmt.Sprintf("event@%d", len(at)+len(over)+2)})
	if e.body[1].Cursor != cursorAfter(body, 2) {
		t.Errorf("the long line's cursor %q, want %q", e.body[1].Cursor, cursorAfter(body, 2))
	}
}
