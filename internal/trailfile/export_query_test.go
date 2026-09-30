package trailfile

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestACursorResumesAfterItsLine: an export after a cursor starts at the
// next line, and a cursor survives appends and a rename, since the file's
// identity is its first line.
func TestACursorResumesAfterItsLine(t *testing.T) {
	body := file(lineE1, lineE2, lineE3)
	q := query()
	q.After = cursorAfter(body, 1)
	e := runExport(t, body, false, q)
	equal(t, "after line 1", e.shorts(), []string{fmt.Sprintf("event@%d", offsetOf(body, 2)), fmt.Sprintf("event@%d", offsetOf(body, 3))})
	if e.result.ScannedBytes != int64(len(body)-offsetOf(body, 2)) {
		t.Errorf("scanned %d bytes, want the two lines after the cursor", e.result.ScannedBytes)
	}
	q.After = cursorAfter(body, 3)
	e = runExport(t, body, false, q)
	if len(e.body) != 0 || e.result.NextCursor != q.After || !e.result.EndReached {
		t.Errorf("after the last line: %v, trailer %+v; want nothing and the same cursor", e.shorts(), e.result)
	}
	grown := body + file(lineMajor2)
	e = runExport(t, grown, false, q)
	equal(t, "after an append", e.shorts(), []string{fmt.Sprintf("gap@%d:unsupported_version", len(body))})
	renamed := source(grown, false)
	renamed.Name = "rotated.jsonl"
	if _, err := Export(renamed, q, &bytes.Buffer{}); err != nil {
		t.Errorf("the same content under another name: %v", err)
	}
}

// TestACursorThatCannotNameALineHereIsRefused: a cursor from another file,
// past the end, off a line boundary or after a line that changed is refused
// before anything is written.
func TestACursorThatCannotNameALineHereIsRefused(t *testing.T) {
	body := file(lineE1, lineE2, lineE3)
	good := cursorAfter(body, 2)
	parts := strings.Split(good, ":")
	with := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, ":")
	}
	// The same length and another content where line 2 stood: a file restored
	// and appended since, which only the line's digest tells apart.
	restored := file(lineE1, strings.Replace(lineE2, `"e2"`, `"x2"`, 1), lineE3)
	cases := map[string]struct {
		body, cursor string
		want         error
	}{
		"another file":          {file(lineE3, lineE2), good, ErrCursorOtherFile},
		"past the end":          {body, with(2, fmt.Sprint(len(body)+1)), ErrCursorPastEnd},
		"past the last newline": {body + partialTail, with(2, fmt.Sprint(len(body)+len(partialTail))), ErrCursorPastEnd},
		"off a line boundary":   {body, with(2, fmt.Sprint(offsetOf(body, 3)-1)), ErrCursorOffLine},
		"after a changed line":  {restored, good, ErrCursorChanged},
		"another line's digest": {body, with(3, strings.Split(cursorAfter(body, 1), ":")[3]), ErrCursorChanged},
		"a second version":      {body, with(0, "v2"), ErrCursorMalformed},
		"capital hex":           {body, with(1, strings.ToUpper(parts[1])), ErrCursorMalformed},
		"a short digest":        {body, with(3, parts[3][1:]), ErrCursorMalformed},
		"a leading zero":        {body, with(2, "0"+parts[2]), ErrCursorMalformed},
		"offset zero":           {body, with(2, "0"), ErrCursorMalformed},
		"a signed offset":       {body, with(2, "+"+parts[2]), ErrCursorMalformed},
		"an offset past int64":  {body, with(2, "9223372036854775808"), ErrCursorMalformed},
		"a fifth part":          {body, good + ":0", ErrCursorMalformed},
		"nothing but v1":        {body, "v1", ErrCursorMalformed},
	}
	for name, c := range cases {
		q := query()
		q.After = c.cursor
		var out bytes.Buffer
		if _, err := Export(source(c.body, false), q, &out); !errors.Is(err, c.want) || out.Len() != 0 {
			t.Errorf("%s: Export = %v with %d bytes written, want %v and nothing written", name, err, out.Len(), c.want)
		}
	}
	q := query()
	q.After = good
	if _, err := Export(source(body, false), q, &bytes.Buffer{}); err != nil {
		t.Errorf("the unaltered cursor: %v", err)
	}
}

// TestTheLimitCountsEveryRecord: the limit stops before the record past it,
// whatever its type, and the next export starts there.
func TestTheLimitCountsEveryRecord(t *testing.T) {
	body := file(lineE1, lineUnknown, lineE1)
	q := query()
	q.Limit = 2
	e := runExport(t, body, false, q)
	equal(t, "limit 2", e.shorts(), []string{"event@0", fmt.Sprintf("gap@%d:malformed", offsetOf(body, 2))})
	if e.result.EndReached || e.result.NextCursor != cursorAfter(body, 2) {
		t.Errorf("limit 2: trailer %+v, want the end not reached and the cursor after line 2", e.result)
	}
	q.Limit = 3
	if e = runExport(t, body, false, q); len(e.body) != 3 || !e.result.EndReached {
		t.Errorf("limit 3: %v, trailer %+v", e.shorts(), e.result)
	}
}

// TestALinePassedByTakesNoRecord: with the limit spent, a line the filters
// pass by is still read, so the end is reached and the cursor moves past it.
func TestALinePassedByTakesNoRecord(t *testing.T) {
	body := file(lineE1, lineE3)
	q := query()
	q.Limit, q.Requests = 1, []string{"r1"}
	e := runExport(t, body, false, q)
	equal(t, "limit 1, the second line passed by", e.shorts(), []string{"event@0"})
	if e.trailer.EndReached == nil || !*e.trailer.EndReached || e.trailer.NextCursor != cursorAfter(body, 2) {
		t.Errorf("export written:\n%swant a trailer with end_reached true and next_cursor after line 2", e.raw)
	}
	if !e.result.EndReached || e.result.NextCursor != cursorAfter(body, 2) || e.result.ScannedBytes != int64(len(body)) {
		t.Errorf("trailer returned %+v, want the end reached after both lines", e.result)
	}
}

// TestTheTailsGapTakesARecord: with no room left for the tail's gap the end
// is not reached, and the next export reports it.
func TestTheTailsGapTakesARecord(t *testing.T) {
	torn := file(lineE1, lineE2) + partialTail
	q := query()
	q.Limit = 2
	e := runExport(t, torn, false, q)
	if len(e.body) != 2 || e.result.EndReached || e.result.Gaps != 0 || e.result.TailBytes != int64(len(partialTail)) {
		t.Errorf("no room for the tail: %v, trailer %+v", e.shorts(), e.result)
	}
	q.Limit, q.After = 1, e.result.NextCursor
	e = runExport(t, torn, false, q)
	equal(t, "the tail next", e.shorts(), []string{fmt.Sprintf("gap@%d:partial_tail", len(torn)-len(partialTail))})
	if !e.result.EndReached {
		t.Errorf("the tail next: trailer %+v", e.result)
	}
}

// TestTheLimitsBounds: a limit under 1 or over MaxExportLimit is refused, and
// MaxExportLimit itself is not.
func TestTheLimitsBounds(t *testing.T) {
	body := file(lineE1)
	for _, limit := range []int{0, -1, MaxExportLimit + 1} {
		q := query()
		q.Limit = limit
		if _, err := Export(source(body, false), q, &bytes.Buffer{}); !errors.Is(err, ErrQuery) {
			t.Errorf("limit %d: Export = %v, want ErrQuery", limit, err)
		}
	}
	q := query()
	q.Limit = MaxExportLimit
	if _, err := Export(source(body, false), q, &bytes.Buffer{}); err != nil {
		t.Errorf("limit %d: %v", MaxExportLimit, err)
	}
	if DefaultExportLimit != 1000 || MaxExportLimit != 100_000 {
		t.Errorf("limits %d and %d, want the documented 1000 and 100000", DefaultExportLimit, MaxExportLimit)
	}
}

// TestTheByteBoundStopsBeforeTheLineThatCrossesIt: whole lines up to the
// bound are read; a first line past it cannot be read under it at all.
func TestTheByteBoundStopsBeforeTheLineThatCrossesIt(t *testing.T) {
	body := file(lineE1, lineE2, lineE3)
	two := int64(offsetOf(body, 3))
	for bound, want := range map[int64]int{two: 2, two - 1: 1, int64(len(body)): 3, 0: 3} {
		q := query()
		q.MaxBytes = bound
		e := runExport(t, body, false, q)
		if len(e.body) != want || e.result.EndReached != (want == 3) || e.result.NextCursor != cursorAfter(body, want) {
			t.Errorf("bound %d: %v, trailer %+v; want %d records", bound, e.shorts(), e.result, want)
		}
	}
	q := query()
	q.MaxBytes = int64(len(lineE1))
	if _, err := Export(source(body, false), q, &bytes.Buffer{}); !errors.Is(err, ErrByteBound) {
		t.Errorf("a bound under the first line: Export = %v, want ErrByteBound", err)
	}
	q.MaxBytes = -1
	if _, err := Export(source(body, false), q, &bytes.Buffer{}); !errors.Is(err, ErrQuery) {
		t.Errorf("a negative bound: Export = %v, want ErrQuery", err)
	}
}

// TestFiltersNarrowEventsAndNeverGaps: an event is written when it matches
// one value of every filter given; a gap is written whatever the filters,
// since what a line that is not an event belongs to is unknown.
func TestFiltersNarrowEventsAndNeverGaps(t *testing.T) {
	body := file(lineE1, lineE2, lineE3, lineMajor2, lineE1)
	o := func(n int) string { return fmt.Sprintf("@%d", offsetOf(body, n)) }
	gap := "gap" + o(4) + ":unsupported_version"
	for name, c := range map[string]struct {
		q    Query
		want []string
	}{
		"a request":         {Query{Requests: []string{"r2"}}, []string{"event" + o(3), gap}},
		"either request":    {Query{Requests: []string{"r2", "r1"}}, []string{"event@0", "event" + o(2), "event" + o(3), gap, "duplicate" + o(5) + ":e1@0"}},
		"a run":             {Query{Runs: []string{"run1"}}, []string{"event@0", "event" + o(2), gap, "duplicate" + o(5) + ":e1@0"}},
		"a tenant":          {Query{Tenants: []string{"t2"}}, []string{"event" + o(3), gap}},
		"a project":         {Query{Projects: []string{"p2"}}, []string{"event" + o(3), gap}},
		"a kind":            {Query{Kinds: []string{"EVENT_KIND_POLICY_DECIDED"}}, []string{"event" + o(2), gap}},
		"every filter":      {Query{Requests: []string{"r1"}, Kinds: []string{"EVENT_KIND_ACTION_PROPOSED"}}, []string{"event@0", gap, "duplicate" + o(5) + ":e1@0"}},
		"filters that miss": {Query{Requests: []string{"r1"}, Tenants: []string{"t2"}}, []string{gap}},
	} {
		c.q.Limit = DefaultExportLimit
		e := runExport(t, body, false, c.q)
		equal(t, name, e.shorts(), c.want)
		if e.result.NextCursor != cursorAfter(body, 5) || !e.result.EndReached || e.result.ScannedBytes != int64(len(body)) {
			t.Errorf("%s: trailer %+v, want every line read", name, e.result)
		}
	}
	for name, q := range map[string]Query{
		"an unknown kind":     {Kinds: []string{"EVENT_KIND_TELEPORTED"}},
		"the unspecified one": {Kinds: []string{"EVENT_KIND_UNSPECIFIED"}},
		"a kind by number":    {Kinds: []string{"2"}},
		"an empty request":    {Requests: []string{""}},
		"an empty run":        {Runs: []string{"run1", ""}},
		"an empty tenant":     {Tenants: []string{""}},
		"an empty project":    {Projects: []string{""}},
	} {
		q.Limit = DefaultExportLimit
		var out bytes.Buffer
		if _, err := Export(source(body, false), q, &out); !errors.Is(err, ErrQuery) || out.Len() != 0 {
			t.Errorf("%s: Export = %v, want ErrQuery and nothing written", name, err)
		}
	}
}

// TestTheHeaderRepeatsTheQueryAsUnderstood: the cursor, the limit, the byte
// bound and each filter in the order given.
func TestTheHeaderRepeatsTheQueryAsUnderstood(t *testing.T) {
	body := file(lineE1, lineE2)
	q := Query{After: cursorAfter(body, 1), Limit: 7, MaxBytes: 4096, Requests: []string{"r1", "r0"}, Runs: []string{"run1"},
		Tenants: []string{"t1"}, Projects: []string{"p1"}, Kinds: []string{"EVENT_KIND_POLICY_DECIDED"}}
	e := runExport(t, body, false, q)
	want := `{"after":"` + q.After + `","limit":7,"max_bytes":4096,"request":["r1","r0"],"run":["run1"],"tenant":["t1"],` +
		`"project":["p1"],"kind":["EVENT_KIND_POLICY_DECIDED"]}`
	if string(e.header.Query) != want {
		t.Errorf("query %s, want %s", e.header.Query, want)
	}
	if e = runExport(t, body, false, query()); string(e.header.Query) != `{"limit":1000}` {
		t.Errorf("an unbounded query reads %s", e.header.Query)
	}
}

// TestAConflictIsAGapWhateverTheFilters: two lines with one event id and
// other content are a conflict whichever of them the filters pass, and an
// exact repeat of a line the filters passed by stays silent.
func TestAConflictIsAGapWhateverTheFilters(t *testing.T) {
	far := `{"eventId":"e1","kind":"EVENT_KIND_POLICY_DECIDED","requestId":"r9","runId":"run9","projectId":"p9","tenantId":"t9","schemaVersion":"1.0"}`
	body := file(lineE1, far, lineE1, lineE2)
	o := func(n int) string { return fmt.Sprintf("@%d", offsetOf(body, n)) }
	conflict := "gap" + o(2) + ":conflicting_event_id"
	for name, c := range map[string]struct {
		q    Query
		want []string
	}{
		"no filter":           {Query{}, []string{"event@0", conflict, "duplicate" + o(3) + ":e1@0", "event" + o(4)}},
		"the other request":   {Query{Requests: []string{"r9"}}, []string{conflict}},
		"the other run":       {Query{Runs: []string{"run9"}}, []string{conflict}},
		"the other tenant":    {Query{Tenants: []string{"t9"}}, []string{conflict}},
		"the other project":   {Query{Projects: []string{"p9"}}, []string{conflict}},
		"the other kind":      {Query{Kinds: []string{"EVENT_KIND_POLICY_DECIDED"}}, []string{conflict, "event" + o(4)}},
		"the first request":   {Query{Requests: []string{"r1"}}, []string{"event@0", conflict, "duplicate" + o(3) + ":e1@0", "event" + o(4)}},
		"the first run":       {Query{Runs: []string{"run1"}}, []string{"event@0", conflict, "duplicate" + o(3) + ":e1@0", "event" + o(4)}},
		"the first tenant":    {Query{Tenants: []string{"t1"}}, []string{"event@0", conflict, "duplicate" + o(3) + ":e1@0", "event" + o(4)}},
		"the first project":   {Query{Projects: []string{"p1"}}, []string{"event@0", conflict, "duplicate" + o(3) + ":e1@0", "event" + o(4)}},
		"the first kind":      {Query{Kinds: []string{"EVENT_KIND_ACTION_PROPOSED"}}, []string{"event@0", conflict, "duplicate" + o(3) + ":e1@0"}},
		"filters that miss":   {Query{Requests: []string{"r9"}, Tenants: []string{"t1"}}, []string{conflict}},
		"after the first one": {Query{After: cursorAfter(body, 1), Requests: []string{"r1"}}, []string{"gap" + o(3) + ":conflicting_event_id", "event" + o(4)}},
	} {
		c.q.Limit = DefaultExportLimit
		e := runExport(t, body, false, c.q)
		equal(t, name, e.shorts(), c.want)
		if e.result.Gaps != strings.Count(strings.Join(c.want, " "), "gap@") {
			t.Errorf("%s: trailer %+v counts another number of gaps", name, e.result)
		}
	}
}

// TestDuplicatesAreFoundWithinOneExport: an export after a cursor does not see
// the lines before it, so a repeat or a conflict of one of them is an event,
// and the trailer says the scope is the export; within the export a repeat is
// a duplicate and a conflict a gap as anywhere else.
func TestDuplicatesAreFoundWithinOneExport(t *testing.T) {
	body := file(lineE1, lineE2, lineE1Other, lineE1, lineE1Other)
	q := query()
	q.After = cursorAfter(body, 2)
	e := runExport(t, body, false, q)
	equal(t, "after line 2", e.shorts(), []string{
		fmt.Sprintf("event@%d", offsetOf(body, 3)),
		fmt.Sprintf("gap@%d:conflicting_event_id", offsetOf(body, 4)),
		fmt.Sprintf("duplicate@%d:e1@%d", offsetOf(body, 5), offsetOf(body, 3)),
	})
	if e.trailer.DedupScope != "export" || e.result.Duplicates != 1 || e.result.Gaps != 1 {
		t.Errorf("trailer %+v, record %+v; want the scope the export, one duplicate and one gap", e.result, e.trailer)
	}
	q.After = cursorAfter(body, 3)
	q.Limit = 1
	e = runExport(t, body, false, q)
	equal(t, "a repeat of a line before the cursor", e.shorts(), []string{fmt.Sprintf("event@%d", offsetOf(body, 4))})
	if e.trailer.DedupScope != "export" || e.result.Duplicates != 0 || e.result.Gaps != 0 {
		t.Errorf("trailer %+v; want the scope the export and nothing found", e.trailer)
	}
}
