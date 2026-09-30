package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// step is one event of a hand-written lifecycle: its kind without the
// EVENT_KIND_ prefix, and the members it carries beyond the common ones.
type step struct{ kind, extra string }

// evt writes one event as the collector would, in tenant t and project p.
func evt(req, id, prev, kind, extra string) string {
	s := `{"eventId":"` + id + `","kind":"EVENT_KIND_` + kind + `","requestId":"` + req + `","runId":"run-` + req +
		`","projectId":"p","tenantId":"t","schemaVersion":"1.0","enforcementMode":"ENFORCEMENT_MODE_ENFORCE"`
	if prev != "" {
		s += `,"prevEventId":"` + prev + `"`
	}
	return s + extra + "}"
}

// chain links steps into one request's events, ids req-1, req-2 and so on.
func chain(req string, steps ...step) []string {
	out := make([]string, len(steps))
	prev := ""
	for i, s := range steps {
		id := fmt.Sprintf("%s-%d", req, i+1)
		out[i] = evt(req, id, prev, s.kind, s.extra)
		prev = id
	}
	return out
}

func proposed(name string) string { return `,"proposed":{"action":{"name":"` + name + `"}}` }

func decided(verdict string, codes ...string) string {
	return `,"decision":{"verdict":"VERDICT_` + verdict + `","reasonCodes":["` + strings.Join(codes, `","`) + `"]}`
}

func approval(state string) string { return `,"approval":{"state":"APPROVAL_STATE_` + state + `"}` }

func execution(id string) string { return `,"executionId":"` + id + `"` }

// ended is the event that closes execution id with a result of status.
func ended(id, status string) string {
	return execution(id) + `,"result":{"executionId":"` + id + `","status":"RESULT_STATUS_` + status + `"}`
}

var (
	allowed = []step{{"ACTION_PROPOSED", proposed("read_order")}, {"POLICY_DECIDED", decided("ALLOW", "RULE_ALLOW")},
		{"ACTION_STARTED", execution("x1")}, {"ACTION_COMPLETED", ended("x1", "SUCCESS")}}
	denied = []step{{"ACTION_PROPOSED", proposed("refund")}, {"POLICY_DECIDED", decided("DENY", "RULE_DENY")},
		{"ACTION_BLOCKED", decided("DENY", "RULE_DENY")}}
	approved = []step{{"ACTION_PROPOSED", proposed("update_order")},
		{"POLICY_DECIDED", decided("REQUIRE_APPROVAL", "APPROVAL_REQUIRED")},
		{"APPROVAL_REQUESTED", approval("PENDING")}, {"APPROVAL_DECIDED", approval("APPROVED")},
		{"ACTION_STARTED", execution("x2")}, {"ACTION_COMPLETED", ended("x2", "SUCCESS")}}
	rejected = []step{{"ACTION_PROPOSED", proposed("update_order")},
		{"POLICY_DECIDED", decided("REQUIRE_APPROVAL", "APPROVAL_REQUIRED")},
		{"APPROVAL_REQUESTED", approval("PENDING")}, {"APPROVAL_DECIDED", approval("REJECTED")},
		{"ACTION_BLOCKED", decided("REQUIRE_APPROVAL", "APPROVAL_REQUIRED")}}
	expired = []step{{"ACTION_PROPOSED", proposed("update_order")},
		{"POLICY_DECIDED", decided("REQUIRE_APPROVAL", "APPROVAL_REQUIRED")},
		{"APPROVAL_REQUESTED", approval("PENDING")}, {"APPROVAL_EXPIRED", approval("EXPIRED")},
		{"ACTION_BLOCKED", decided("REQUIRE_APPROVAL", "APPROVAL_REQUIRED")}}
	failed = []step{{"ACTION_PROPOSED", proposed("export_orders")}, {"POLICY_DECIDED", decided("ALLOW", "RULE_ALLOW")},
		{"ACTION_STARTED", execution("x3")}, {"ACTION_FAILED", ended("x3", "FAILURE")}}
	undetermined = []step{{"ACTION_PROPOSED", proposed("export_orders")},
		{"POLICY_DECIDED", decided("INDETERMINATE", "RULE_UNDETERMINED", "PDP_TIMEOUT")},
		{"ACTION_BLOCKED", decided("INDETERMINATE", "RULE_UNDETERMINED", "PDP_TIMEOUT")}}
	held = []step{{"ACTION_PROPOSED", proposed("update_order")},
		{"POLICY_DECIDED", decided("REQUIRE_APPROVAL", "APPROVAL_REQUIRED")},
		{"APPROVAL_REQUESTED", approval("PENDING")}}
)

// exportText builds an export the way the exporter frames one: a header,
// records at rising offsets 100 apart, and a trailer counting them.
type exportText struct {
	lines           []string
	offset          int64
	events, gaps, n int
}

func newExport(version string) *exportText {
	return &exportText{lines: []string{
		`{"type":"header","format":"` + exportFormat + `","version":"` + version + `","file":"a.trail","query":{"limit":1000}}`}}
}

func (x *exportText) event(events ...string) *exportText {
	for _, e := range events {
		x.lines = append(x.lines, fmt.Sprintf(`{"type":"event","offset":%d,"cursor":"c%d","event":%s}`, x.offset, x.offset, e))
		x.offset += 100
		x.events++
	}
	return x
}

func (x *exportText) gap(reason string) *exportText {
	x.lines = append(x.lines, fmt.Sprintf(`{"type":"gap","offset":%d,"cursor":"c%d","reason":"%s"}`, x.offset, x.offset, reason))
	x.offset += 100
	x.gaps++
	return x
}

func (x *exportText) duplicate(id string, first int64) *exportText {
	x.lines = append(x.lines, fmt.Sprintf(`{"type":"duplicate","offset":%d,"event_id":"%s","first_offset":%d}`, x.offset, id, first))
	x.offset += 100
	x.n++
	return x
}

func (x *exportText) raw(line string) *exportText {
	x.lines = append(x.lines, line)
	return x
}

// cut is the export without its trailer.
func (x *exportText) cut() string { return strings.Join(x.lines, "\n") + "\n" }

// someCursor is spelled as a v1 cursor is, and names no file.
var someCursor = "v1:" + strings.Repeat("a", 64) + ":2100:" + strings.Repeat("b", 64)

// trailerLine is the trailer the exporter would write after these records
// while no writer holds the file.
func (x *exportText) trailerLine(endReached bool, tail int) string {
	return x.heldTrailerLine(endReached, tail, false)
}

func (x *exportText) heldTrailerLine(endReached bool, tail int, held bool) string {
	return fmt.Sprintf(`{"type":"trailer","next_cursor":"%s","end_reached":%t,"tail_bytes":%d,"writer_held":%t,`+
		`"counts":{"event":%d,"gap":%d,"duplicate":%d},"scanned_bytes":%d,"dedup_scope":"export"}`,
		someCursor, endReached, tail, held, x.events, x.gaps, x.n, x.offset)
}

// partialTail is the gap the exporter writes for the bytes after the last
// newline when no writer holds the file: no newline ends them, so no cursor.
func (x *exportText) partialTail() *exportText {
	x.lines = append(x.lines, fmt.Sprintf(`{"type":"gap","offset":%d,"reason":"partial_tail"}`, x.offset))
	x.gaps++
	return x
}

func (x *exportText) whole() string { return x.cut() + x.trailerLine(true, 0) + "\n" }

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// outcome is what one run printed and returned.
type outcome struct {
	code           int
	stdout, stderr string
}

func report(t *testing.T, in string, args ...string) outcome {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(in), &stdout, &stderr)
	return outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

const columns = "tenant\tproject\trequest\trun\taction\tverdict\treasons\theld\tapproval\tend\tnote"

// table splits stdout into its rows and its totals line, and fails when the
// column line does not lead it.
func table(t *testing.T, stdout string) (rows []string, totals string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) < 2 || lines[0] != columns {
		t.Fatalf("stdout is not a table with a totals line:\n%s", stdout)
	}
	return lines[1 : len(lines)-1], lines[len(lines)-1]
}
