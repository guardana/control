package main

import (
	"strings"
	"testing"
)

// nextSegment is the committed export as the next segment of the same log
// would be exported after cursor: another source in its header and every
// cursor, asked after the state's cursor, its first record at offset 0.
func nextSegment(t *testing.T, export, cursor string) string {
	t.Helper()
	in := ofAnotherFile(export)(t)
	query := `"query":{"limit":1000}`
	if !strings.Contains(in, query) || !strings.Contains(in, `{"type":"event","offset":0,`) {
		t.Fatalf("%s is not an export from its file's first line", export)
	}
	return strings.Replace(in, query, `"query":{"after":"`+cursor+`","limit":1000}`, 1)
}

// TestAStateFollowsALogOntoItsNextSegment: after the state's cursor, an
// export of another source that starts at its first line is taken, and the
// state moves to that source; the same export starting past its first line
// is another file and refused.
func TestAStateFollowsALogOntoItsNextSegment(t *testing.T) {
	dir := newState(t)
	if got := consume(t, dir, fixture(t, "outage-1.jsonl")); got.code == 2 {
		t.Fatalf("the first export: exit 2, %s", got.stderr)
	}
	cursor := trailerOf(t, "outage-1.jsonl")
	if got := consume(t, dir, nextSegment(t, "outage-1.jsonl", cursor)); got.code == 2 {
		t.Fatalf("the next segment was refused: %s", got.stderr)
	}
	if s := stateOf(t, dir); !strings.Contains(s, `"source":"`+conflictSource+`"`) || strings.Contains(s, outageSource) {
		t.Errorf("the state did not move to the next segment's source:\n%s", s)
	}

	dir = newState(t)
	if got := consume(t, dir, fixture(t, "outage-1.jsonl")); got.code == 2 {
		t.Fatalf("the first export: exit 2, %s", got.stderr)
	}
	past := strings.Replace(nextSegment(t, "outage-1.jsonl", cursor), `{"type":"event","offset":0,`, `{"type":"event","offset":1,`, 1)
	if got := consume(t, dir, past); got.code != 2 || !strings.Contains(got.stderr, "another file") {
		t.Errorf("another source not at its first line: exit %d, %s; want 2 naming another file", got.code, got.stderr)
	}
}

// gapsSource is the source of the committed gaps exports.
const gapsSource = "84e72ed562caecb9f90bea851beaa26eb947ad6bca1fa520becc800e3ce4cb4a"

// TestANextSegmentsGapAtTheLastOnesTailOffsetIsRaised: the gap after the last
// newline is alerted once while it stays where it was in one file; in the
// next segment of the log a gap at that offset is another gap, and alerted.
func TestANextSegmentsGapAtTheLastOnesTailOffsetIsRaised(t *testing.T) {
	dir := newState(t)
	if got := consume(t, dir, fixture(t, "gaps.jsonl")); !strings.Contains(alertLines(got.stderr), `"offset":1540,"note":"partial_tail"`) {
		t.Fatalf("the first export raised %q, want the tail gap at 1540", alertLines(got.stderr))
	}
	if got := consume(t, dir, fixture(t, "gaps-2.jsonl")); got.code != 0 || alertLines(got.stderr) != "" {
		t.Fatalf("the same tail gap again in the same file: exit %d, alerts %q; want 0 and none", got.code, alertLines(got.stderr))
	}
	in := fixture(t, "gaps.jsonl")
	other := strings.Repeat("c", 64)
	for _, member := range []string{`"source":"`, `"cursor":"v1:`, `"next_cursor":"v1:`} {
		in = strings.ReplaceAll(in, member+gapsSource, member+other)
	}
	query := `"query":{"limit":1000}`
	if !strings.Contains(in, query) || strings.Contains(in, `"source":"`+gapsSource) || strings.Contains(in, `"cursor":"v1:`+gapsSource) {
		t.Fatal("gaps.jsonl is not an export of one file from its first line")
	}
	in = strings.Replace(in, query, `"query":{"after":"`+trailerOf(t, "gaps.jsonl")+`","limit":1000}`, 1)
	got := consume(t, dir, in)
	if got.code != 1 || !strings.Contains(alertLines(got.stderr), `{"v":1,"alert":"evidence_gap","offset":1540,"note":"partial_tail"}`) {
		t.Errorf("the next segment's tail gap at 1540: exit %d, alerts %q; want it raised", got.code, alertLines(got.stderr))
	}
}

// prunedAlertLine is the alert of an export that names no cursor and counts
// segments pruned before it.
const prunedAlertLine = `{"v":1,"alert":"evidence_gap","offset":0,"note":"pruned"}`

// TestAStatesFirstExportAfterPrunedSegmentsRaisesAGap: a state's first
// export, which names no cursor, raises evidence_gap noted pruned when its
// header counts segments pruned before it, and none when it counts none.
func TestAStatesFirstExportAfterPrunedSegmentsRaisesAGap(t *testing.T) {
	plain := fixture(t, "outage-1.jsonl")
	if got := consume(t, newState(t), plain); got.code == 2 || strings.Contains(got.stderr, `"note":"pruned"`) {
		t.Errorf("a first export counting nothing pruned: exit %d, %s", got.code, got.stderr)
	}
	pruned := strings.Replace(plain, `"pruned_before":0,`, `"pruned_before":2,`, 1)
	if got := consume(t, newState(t), pruned); got.code != 1 || !strings.Contains(alertLines(got.stderr), prunedAlertLine+"\n") {
		t.Errorf("a first export counting two segments pruned: exit %d, alerts %q; want %s", got.code, alertLines(got.stderr), prunedAlertLine)
	}
}

// TestAResumedExportAfterPrunedSegmentsRaisesNoGap: an export after the
// state's cursor counts in pruned_before segments the state read before an
// operator pruned them, and raises no gap for them; the state's first export
// raised one for the count it began with.
func TestAResumedExportAfterPrunedSegmentsRaisesNoGap(t *testing.T) {
	dir := newState(t)
	first := strings.Replace(fixture(t, "outage-1.jsonl"), `"pruned_before":0,`, `"pruned_before":1,`, 1)
	if got := consume(t, dir, first); !strings.Contains(alertLines(got.stderr), prunedAlertLine+"\n") {
		t.Fatalf("the first export counting one segment pruned raised %q, want %s", alertLines(got.stderr), prunedAlertLine)
	}
	next := strings.Replace(nextSegment(t, "outage-1.jsonl", trailerOf(t, "outage-1.jsonl")), `"pruned_before":0,`, `"pruned_before":2,`, 1)
	if got := consume(t, dir, next); got.code == 2 || strings.Contains(got.stderr, `"note":"pruned"`) {
		t.Errorf("the next segment after the state's cursor counting two pruned: exit %d, %s; want no pruned gap", got.code, got.stderr)
	}
}
