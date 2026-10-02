package main

import (
	"fmt"
	"strings"
	"testing"
)

// proposals is n requests q0, q1 and so on, each proposed and still open.
func proposals(n int) []string {
	out := make([]string, n)
	for i := range out {
		req := fmt.Sprintf("q%d", i)
		out[i] = evt(req, req+"-1", "", "ACTION_PROPOSED", proposed("read_order"))
	}
	return out
}

// TestOpenBound: 10 000 open requests are all followed; one more drops the
// oldest with an open_bound alert where it was raised, and its next event
// reads as broken while still raising what it calls for.
func TestOpenBound(t *testing.T) {
	decidedQ0 := evt("q0", "q0-2", "q0-1", "POLICY_DECIDED", decided("INDETERMINATE", "PDP_TIMEOUT"))
	undecided := `{"v":1,"alert":"indeterminate","tenant":"t","project":"p","request":"q0","run":"run-q0","event":"q0-2",` +
		`"offset":%d,"verdict":"INDETERMINATE","reasons":["PDP_TIMEOUT"]}` + "\n"
	for _, tc := range []struct {
		open                 int
		code                 int
		alerts, totals, next string
		nextAlerts           string
	}{
		{open: 10_000, totals: "totals: requests 10000, completed 0, failed 0, aborted 0, blocked 0, open 10000, unknown 0; " +
			"gaps 0, duplicates 0, conflicting 0, refused 0; trailer end reached",
			next: columns + "\n" + "totals: requests 10000, completed 0, failed 0, aborted 0, blocked 0, open 10000, unknown 0; " +
				"gaps 0, duplicates 0, conflicting 0, refused 0; trailer end reached\n",
			nextAlerts: undecided},
		{open: 10_001, code: 1, alerts: `{"v":1,"alert":"open_bound","tenant":"t","project":"p","request":"q0","run":"run-q0",` +
			`"offset":%d,"note":"more than 10000 requests are open: the oldest is no longer followed, and its later events read as broken"}` + "\n",
			totals: "totals: requests 10000, completed 0, failed 0, aborted 0, blocked 0, open 10000, unknown 0; " +
				"gaps 0, duplicates 0, conflicting 0, refused 0; trailer end reached",
			next: columns + "\n" +
				"t\tp\tq0\trun-q0\t-\tINDETERMINATE\tPDP_TIMEOUT\t-\tno\t-\tunknown\tevent q0-2 follows q0-1, of a request no longer followed\n" +
				"totals: requests 10001, completed 0, failed 0, aborted 0, blocked 0, open 10000, unknown 1; " +
				"gaps 0, duplicates 0, conflicting 0, refused 0; trailer end reached\n",
			nextAlerts: undecided + `{"v":1,"alert":"lifecycle_unknown","tenant":"t","project":"p","request":"q0","run":"run-q0","event":"q0-2",` +
				`"offset":%d,"note":"event q0-2 follows q0-1, of a request no longer followed"}` + "\n"},
	} {
		t.Run(fmt.Sprint(tc.open), func(t *testing.T) {
			lines := proposals(tc.open)
			dir := newState(t)
			got := consume(t, dir, framed("q.trail", lines, 0, len(lines), 100_000))
			_, totals := table(t, got.stdout)
			var last, end int
			for _, l := range lines {
				last, end = end, end+len(l)+1
			}
			want := strings.ReplaceAll(tc.alerts, "%d", fmt.Sprint(last))
			if got.code != tc.code || totals != tc.totals || withoutCursors(alertLines(got.stderr)) != want {
				t.Fatalf("%d proposals: exit %d, totals %q, alerts\n%s\nwant %d, %q,\n%s", tc.open, got.code, totals, alertLines(got.stderr), tc.code, tc.totals, want)
			}
			lines = append(lines, decidedQ0)
			got = consume(t, dir, framed("q.trail", lines, len(lines)-1, len(lines), 100_000))
			want = strings.ReplaceAll(tc.nextAlerts, "%d", fmt.Sprint(end))
			if got.code != 1 || got.stdout != tc.next || withoutCursors(alertLines(got.stderr)) != want {
				t.Errorf("q0 decided after: exit %d, stdout\n%s\nalerts\n%s\nwant 1,\n%s\n%s", got.code, got.stdout, alertLines(got.stderr), tc.next, want)
			}
		})
	}
}

// denials is n requests d0, d1 and so on, each denied and blocked, three
// events apiece.
func denials(n int) []string {
	out := make([]string, 0, 3*n)
	for i := range n {
		out = append(out, chain(fmt.Sprintf("d%d", i), denied...)...)
	}
	return out
}

// TestDedupWindowBound: the window keeps the last 20 000 events. A line
// repeated in the next export while its first is the 20 000th from the end is
// a duplicate; once it is the 20 001st it reads as a new event, which here
// starts a request that ended.
func TestDedupWindowBound(t *testing.T) {
	w := chain("w", denied...)
	for _, tc := range []struct {
		findings int
		code     int
		totals   string
	}{
		{findings: 2, totals: "totals: requests 0, completed 0, failed 0, aborted 0, blocked 0, open 0, unknown 0; " +
			"gaps 0, duplicates 1, conflicting 0, refused 0; trailer end reached"},
		{findings: 3, code: 1, totals: "totals: requests 0, completed 0, failed 0, aborted 0, blocked 0, open 0, unknown 0; " +
			"gaps 0, duplicates 0, conflicting 0, refused 0; trailer end reached"},
	} {
		t.Run(fmt.Sprint(tc.findings), func(t *testing.T) {
			lines := append(append([]string(nil), w...), denials(6665)...)
			prev := "d6664-3"
			for i := range tc.findings {
				id := fmt.Sprintf("f%d", i)
				lines = append(lines, evt("d6664", id, prev, "FINDING_RAISED", ""))
				prev = id
			}
			if others := len(lines); others != 20_000+tc.findings-2 {
				t.Fatalf("%d events no request holds open, want %d", others, 20_000+tc.findings-2)
			}
			dir := newState(t)
			if got := consume(t, dir, framed("w.trail", lines, 0, len(lines), 100_000)); got.code != 0 {
				t.Fatalf("the first export: exit %d, stderr %s", got.code, got.stderr)
			}
			lines = append(lines, w[0])
			got := consume(t, dir, framed("w.trail", lines, len(lines)-1, len(lines), 100_000))
			_, totals := table(t, got.stdout)
			if got.code != tc.code || totals != tc.totals {
				t.Errorf("w-1 again: exit %d, totals %q; want %d, %q; stderr %s", got.code, totals, tc.code, tc.totals, got.stderr)
			}
			if tc.code == 1 && !strings.Contains(got.stderr, `"note":"event w-1 starts request w again after it ended"`) {
				t.Errorf("w-1 again past the window: stderr %s", got.stderr)
			}
		})
	}
}
