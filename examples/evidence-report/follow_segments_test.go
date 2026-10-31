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
