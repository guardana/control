package main

import (
	"strings"
	"testing"
)

// TestStrictRecords holds every record to the members the contract gives it,
// each spelled exactly and held once: a lenient decoder takes the last of two
// keys and a key in any case, which lets a record say two things at once.
func TestStrictRecords(t *testing.T) {
	r1 := chain("r1", allowed...)
	x := newExport("1.0").event(r1...)
	header := newExport("1.0").lines[0]
	trailer := x.trailerLine(true, 0)
	withHeader := func(h string) string { return h + "\n" + strings.Join(x.lines[1:], "\n") + "\n" + trailer + "\n" }
	withTrailer := func(tr string) string { return x.cut() + tr + "\n" }
	noTrailer := func(name, in, says string) reportCase {
		return reportCase{name: name, in: in, rows: []string{rowAllowed},
			totals: oneCompleted + "gaps 0, duplicates 0, conflicting 0, refused 1; no trailer read, the export is not whole",
			code:   1, stderr: says}
	}
	// refusedRecord follows the request with one record the trailer counts
	// by the type it names. A record that is not one object names no type.
	refusedRecord := func(name, record, says string) reportCase {
		y := newExport("1.0").event(r1...).raw(record)
		switch {
		case strings.HasPrefix(record, `{"type":"event"`):
			y.events++
		case strings.HasPrefix(record, `{"type":"gap"`):
			y.gaps++
		default:
			y.n++
		}
		return reportCase{name: name, in: y.whole(), rows: []string{rowAllowed},
			totals: oneCompleted + "gaps 0, duplicates 0, conflicting 0, refused 1" + endReached, code: 1, stderr: says}
	}
	firstEvent := x.lines[1]
	cases := []reportCase{
		{name: "a header key held twice", in: withHeader(strings.Replace(header, `"version":"1.0"`, `"version":"2.0","version":"1.0"`, 1)),
			code: 1, stderr: `member "version" appears twice`},
		{name: "a header in capitals", in: withHeader(strings.Replace(strings.Replace(header, `"type"`, `"Type"`, 1), `"format"`, `"FORMAT"`, 1)),
			code: 1, stderr: `differs from "type" only in case`},
		{name: "a header member the contract does not name", in: withHeader(strings.Replace(header, `"file"`, `"granted":true,"file"`, 1)),
			code: 1, stderr: `member "granted"`},
		{name: "a query member the contract does not name", in: withHeader(strings.Replace(header, `"limit":1000`, `"limit":1000,"requests":["r1"]`, 1)),
			code: 1, stderr: `member "requests"`},
		{name: "a query member in another case", in: withHeader(strings.Replace(header, `"limit":1000`, `"limit":1000,"Kind":["X"]`, 1)),
			code: 1, stderr: `differs from "kind" only in case`},
		{name: "a query that is not an object", in: withHeader(strings.Replace(header, `{"limit":1000}`, `[]`, 1)),
			code: 1, stderr: "query"},
		noTrailer("a trailer key held twice", withTrailer(strings.Replace(trailer, `"end_reached":true`, `"end_reached":false,"end_reached":true`, 1)),
			`member "end_reached" appears twice`),
		noTrailer("a trailer key in another case", withTrailer(strings.Replace(trailer, `"end_reached":true`, `"end_reached":false,"END_REACHED":true`, 1)),
			`differs from "end_reached" only in case`),
		noTrailer("a trailer type in capitals", withTrailer(strings.Replace(trailer, `"type"`, `"TYPE"`, 1)),
			`differs from "type" only in case`),
		noTrailer("a trailer member the contract does not name", withTrailer(strings.Replace(trailer, `"writer_held"`, `"complete":true,"writer_held"`, 1)),
			`member "complete"`),
		noTrailer("a count the contract does not name", withTrailer(strings.Replace(trailer, `"duplicate":0}`, `"duplicate":0,"finding":0}`, 1)),
			`member "finding"`),
		noTrailer("a count held twice", withTrailer(strings.Replace(trailer, `"counts":{"event":4`, `"counts":{"event":9,"event":4`, 1)),
			`member "event" appears twice`),
		noTrailer("a negative tail", withTrailer(strings.Replace(trailer, `"tail_bytes":0`, `"tail_bytes":-5`, 1)),
			"tail_bytes"),
		noTrailer("an end not reached with no next cursor", withTrailer(strings.Replace(x.trailerLine(false, 0), `"next_cursor":"`+someCursor+`",`, "", 1)),
			"next_cursor"),
		noTrailer("an end not reached with a next cursor that is not v1", withTrailer(strings.Replace(x.trailerLine(false, 0), someCursor, "c", 1)),
			"next_cursor"),
		noTrailer("a next cursor with a leading zero", withTrailer(strings.Replace(x.trailerLine(false, 0), ":2100:", ":02100:", 1)),
			"next_cursor"),
		noTrailer("a next cursor in capitals", withTrailer(strings.Replace(x.trailerLine(false, 0), "v1:aaaa", "v1:AAAA", 1)),
			"next_cursor"),
		noTrailer("a trailer followed by more on its line", withTrailer(trailer+` {}`), "more follows the object"),
		{name: "an event record key held twice", in: newExport("1.0").event(r1...).raw(strings.Replace(firstEvent, `"offset":0`, `"offset":400,"offset":500`, 1)).whole(),
			rows: []string{rowAllowed}, totals: oneCompleted + "gaps 0, duplicates 0, conflicting 0, refused 1" + endReached,
			code: 1, stderr: `member "offset" appears twice`},
		refusedRecord("an event record key in another case", strings.Replace(strings.Replace(firstEvent, `"offset":0`, `"offset":400`, 1), `"event":{`, `"Event":{`, 1),
			`differs from "event" only in case`),
		refusedRecord("an event record member the contract does not name", `{"type":"event","offset":400,"cursor":"c","event":{},"verified":true}`,
			`member "verified"`),
		refusedRecord("a gap record member the contract does not name", `{"type":"gap","offset":400,"reason":"malformed","event_id":"r1-1"}`,
			`member "event_id"`),
		refusedRecord("a duplicate record key in another case", `{"type":"duplicate","offset":400,"event_id":"r1-2","First_Offset":100}`,
			`differs from "first_offset" only in case`),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { check(t, tc) })
	}
}

// TestTrailerCursor accepts a next cursor spelled as the contract spells one,
// so the refusals above are of the spelling and not of every cursor.
func TestTrailerCursor(t *testing.T) {
	x := newExport("1.0").event(chain("r1", allowed...)...)
	check(t, reportCase{in: x.cut() + x.trailerLine(false, 0) + "\n", rows: []string{rowAllowed},
		totals: oneCompleted + "gaps 0, duplicates 0, conflicting 0, refused 0; trailer end not reached",
		code:   1, stderr: "export again after its next_cursor"})
}

// TestNoRequest holds that an export accounting for no request is not a
// clean report: a filter naming a request that is not there reads the same.
func TestNoRequest(t *testing.T) {
	filtered := strings.Replace(newExport("1.0").whole(), `"limit":1000`, `"limit":1000,"request":["typo"]`, 1)
	for name, in := range map[string]string{"no event": newExport("1.0").whole(), "a filter that passed nothing": filtered} {
		t.Run(name, func(t *testing.T) {
			check(t, reportCase{in: in,
				totals: "totals: requests 0, completed 0, failed 0, aborted 0, blocked 0, open 0, unknown 0; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached,
				code:   1, stderr: "accounts for no request"})
		})
	}
}

// TestConflictAcrossRequests holds that one event id with two contents makes
// both requests unknown when the two lines belong to different requests.
func TestConflictAcrossRequests(t *testing.T) {
	r1 := chain("r1", allowed...)
	r2 := chain("r2", denied...)
	r2[2] = evt("r2", "r1-4", "r2-2", "ACTION_BLOCKED", decided("DENY", "RULE_DENY"))
	check(t, reportCase{in: newExport("1.0").event(r1...).event(r2...).whole(),
		rows: []string{"t\tp\tr1\trun-r1\tread_order\tALLOW\tRULE_ALLOW\t-\tno\t-\tunknown\tevent r1-4 is held twice with different content",
			"t\tp\tr2\trun-r2\trefund\tDENY\tRULE_DENY\tDENY RULE_DENY\tno\t-\tunknown\tevent r1-4 is held twice with different content"},
		totals: "totals: requests 2, completed 0, failed 0, aborted 0, blocked 0, open 0, unknown 2; gaps 0, duplicates 0, conflicting 1, refused 0" + endReached,
		code:   1, stderr: "different content"})
}

// TestGapSaidOnce holds a gap to one line of diagnostics.
func TestGapSaidOnce(t *testing.T) {
	got := report(t, newExport("1.0").event(chain("r1", allowed...)...).gap("malformed").whole())
	if got.code != 1 || strings.Count(got.stderr, "gap") != 1 || !strings.Contains(got.stderr, "a gap at offset 400: malformed") {
		t.Fatalf("exit %d, stderr:\n%s\nwant 1 and the gap said once", got.code, got.stderr)
	}
}
