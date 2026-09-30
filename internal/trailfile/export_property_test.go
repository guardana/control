package trailfile

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// genLine is one line of a trail file: events over a few ids and contents, so
// repeats and conflicts arise, and lines that are gaps.
func genLine(t *rapid.T) string {
	id := rapid.SampledFrom([]string{"a", "b", "c", ""}).Draw(t, "id")
	run := rapid.SampledFrom([]string{"x", "y"}).Draw(t, "run")
	event := `{"eventId":"` + id + `","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r","runId":"` + run +
		`","projectId":"p","tenantId":"t","schemaVersion":"%s"}`
	return rapid.SampledFrom([]string{
		fmt.Sprintf(event, "1.0"), fmt.Sprintf(event, "1.0"), fmt.Sprintf(event, "1.2"),
		fmt.Sprintf(event, "2.0"), `{"nope":1}`, "", "not json",
	}).Draw(t, "line")
}

// TestExportsSplitAnywhereJoinToOneExport: exports resumed from each other's
// cursors under any limits, from the start or from any line, read every line
// once and in order, each export's records those a model judging its lines
// alone expects, types included, since duplicates are found within one export.
func TestExportsSplitAnywhereJoinToOneExport(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lines := rapid.SliceOfN(rapid.Custom(genLine), 1, 12).Draw(t, "lines")
		body := file(lines...)
		torn := rapid.Bool().Draw(t, "torn")
		if torn {
			body += partialTail
		}
		held := rapid.Bool().Draw(t, "held")
		var runs []string
		if rapid.Bool().Draw(t, "filtered") {
			runs = []string{"x"}
		}
		from := rapid.IntRange(0, len(lines)).Draw(t, "from")
		after, start := "", 0
		if from > 0 {
			after, start = cursorAfter(body, from), offsetOf(body, from+1)
		}
		tail := ""
		if torn && !held {
			tail = fmt.Sprintf("gap@%d:%s", offsetOf(body, len(lines)+1), gapPartialTail)
		}

		whole := exportRecords(t, body, held, Query{Limit: MaxExportLimit, Runs: runs})
		if got, want := shorts(whole), judgeLines(body, 0, offsetOf(body, len(lines)+1), runs, tail); got != want {
			t.Fatalf("one export:\n got %s\nwant %s", got, want)
		}
		checkParts(t, body, exportInParts(t, body, held, runs, after, len(lines)), start, offsetOf(body, len(lines)+1), runs, tail)
	})
}

// checkParts holds exports in series to lines read once and in order from
// start to end, the last newline, and each export's records to the model's
// judgement of its lines alone, the tail's gap after the lines of the one
// that reached the end.
func checkParts(t *rapid.T, body string, parts []part, start, end int, runs []string, tail string) {
	for i, p := range parts {
		if p.start != start {
			t.Fatalf("export %d starts at %d, the one before ended at %d", i, p.start, start)
		}
		want := judgeLines(body, p.start, p.end, runs, "")
		if p.endReached && tail != "" {
			want = strings.TrimSpace(want + " " + tail)
		}
		if got := shorts(p.recs); got != want {
			t.Fatalf("export %d of [%d, %d):\n got %s\nwant %s", i, p.start, p.end, got, want)
		}
		checkCursors(t, body, p.recs)
		start = p.end
	}
	if start != end {
		t.Fatalf("the exports end at %d, the last newline at %d", start, end)
	}
}

// part is one export of a series: the lines it read, [start, end), and its
// records.
type part struct {
	start, end int
	endReached bool
	recs       []record
}

// exportInParts exports body after after under drawn limits, each export
// after the cursor the one before returned, until one reaches the end. An
// export that stops short of the end wrote as many records as its limit.
func exportInParts(t *rapid.T, body string, held bool, runs []string, after string, lines int) []part {
	var parts []part
	for n := 0; ; n++ {
		if n > 2*lines+4 {
			t.Fatalf("no end after %d exports", n)
		}
		limit := rapid.IntRange(1, 4).Draw(t, "limit")
		var out strings.Builder
		tr, err := Export(source(body, held), Query{After: after, Limit: limit, Runs: runs}, &out)
		if err != nil {
			t.Fatalf("export %d after %q: %v", n, after, err)
		}
		p := part{start: cursorOffset(t, after), end: cursorOffset(t, tr.NextCursor), endReached: tr.EndReached,
			recs: parseRecords(t, out.String())}
		if len(p.recs) > limit || !tr.EndReached && len(p.recs) != limit {
			t.Fatalf("export %d wrote %d records under a limit of %d, end reached %v", n, len(p.recs), limit, tr.EndReached)
		}
		parts = append(parts, p)
		if tr.EndReached {
			return parts
		}
		after = tr.NextCursor
	}
}

// cursorOffset is the offset a cursor names, 0 for none.
func cursorOffset(t *rapid.T, c string) int {
	if c == "" {
		return 0
	}
	parts := strings.Split(c, ":")
	n, err := strconv.Atoi(parts[2])
	if len(parts) != 4 || err != nil {
		t.Fatalf("cursor %q", c)
	}
	return n
}

// judgeLines is what one export of the whole lines in body[from:to] writes
// under the run filter runs, then extra, judged from the lines genLine draws
// alone.
func judgeLines(body string, from, to int, runs []string, extra string) string {
	seen := map[string]modelFirst{}
	var out []string
	for offset := from; offset < to; {
		line := body[offset : offset+strings.IndexByte(body[offset:], '\n')+1]
		if r := modelRecord(seen, offset, line, runs); r != "" {
			out = append(out, r)
		}
		offset += len(line)
	}
	if extra != "" {
		out = append(out, extra)
	}
	return strings.Join(out, " ")
}

// modelFirst is where the model first read an event id, and that line.
type modelFirst struct {
	offset int
	line   string
}

// modelRecord is the record of the line at offset, empty for none: a line
// that is not one known event is malformed, an event of a major other than 1
// unsupported, and the first line read with an id, written or not, the one
// its later lines are held to.
func modelRecord(seen map[string]modelFirst, offset int, line string, runs []string) string {
	id, run, gap := modelLine(line)
	if gap != "" {
		return fmt.Sprintf("gap@%d:%s", offset, gap)
	}
	f, repeated := seen[id]
	switch {
	case repeated && f.line != line:
		return fmt.Sprintf("gap@%d:%s", offset, gapConflict)
	case !repeated && id != "":
		seen[id] = modelFirst{offset, line}
	}
	switch {
	case runs != nil && run != runs[0]:
		return ""
	case repeated:
		return fmt.Sprintf("duplicate@%d:%s@%d", offset, id, f.offset)
	}
	return fmt.Sprintf("event@%d", offset)
}

// modelLine reads a line genLine draws: its event id and run, or why it is a
// gap.
func modelLine(line string) (id, run, gap string) {
	var ev struct {
		EventID       string `json:"eventId"`
		Kind          string `json:"kind"`
		RequestID     string `json:"requestId"`
		RunID         string `json:"runId"`
		ProjectID     string `json:"projectId"`
		TenantID      string `json:"tenantId"`
		SchemaVersion string `json:"schemaVersion"`
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	switch err := dec.Decode(&ev); {
	case err != nil:
		return "", "", gapMalformed
	case !strings.HasPrefix(ev.SchemaVersion, "1."):
		return "", "", gapVersion
	}
	return ev.EventID, ev.RunID, ""
}

func exportRecords(t *rapid.T, body string, held bool, q Query) []record {
	var out strings.Builder
	if _, err := Export(source(body, held), q, &out); err != nil {
		t.Fatalf("Export = %v", err)
	}
	return parseRecords(t, out.String())
}

// parseRecords is an export's records between its header and its trailer.
func parseRecords(t *rapid.T, out string) []record {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	var recs []record
	for i, line := range lines {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		if (i == 0) != (r.Type == "header") || (i == len(lines)-1) != (r.Type == "trailer") {
			t.Fatalf("a %s record at line %d of %d", r.Type, i+1, len(lines))
		}
		if r.Type != "header" && r.Type != "trailer" {
			recs = append(recs, r)
		}
	}
	return recs
}

// checkCursors holds each record that carries a cursor to the one its line
// ends at.
func checkCursors(t *rapid.T, body string, recs []record) {
	for _, r := range recs {
		if r.Type != "duplicate" && r.Reason != gapPartialTail && r.Cursor != cursorAt(body, *r.Offset) {
			t.Fatalf("record %s carries cursor %s", r.short(), r.Cursor)
		}
	}
}

// cursorAt is the cursor of the line starting at offset in body.
func cursorAt(body string, offset int64) string {
	return cursorAfter(body, strings.Count(body[:offset], "\n")+1)
}

func shorts(recs []record) string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.short())
	}
	return strings.Join(out, " ")
}
