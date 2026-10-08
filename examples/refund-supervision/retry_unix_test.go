//go:build unix

package refundsupervision

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/findinglog"
	"google.golang.org/protobuf/encoding/protojson"
)

// retryRequests are the plane's request ids of the calls of the retried
// refund.
type retryRequests struct{ read, export, refund, update string }

// retryWork is the refund task gone wrong: the customer's order read, the
// customer's orders exported with a person's approval, a refund of another
// customer's order denied, and that order then marked refunded instead.
func (ra *refundAgent) retryWork(t *testing.T, tr tree) retryRequests {
	t.Helper()
	var rq retryRequests
	own := ra.timed(t, "read_order", map[string]string{"id": "ord-1"})
	expectOwnOrder(t, "the read of the customer's order", own)
	rq.read = own.meta("request_id")
	rq.export = ra.approved(t, tr, "export_orders", map[string]string{"id": "cus-1"}, "customer cus-1: ord-1, ord-2, ord-3")
	denied := ra.timed(t, "refund", map[string]string{"id": "ord-2", "amount": "12.00"})
	if !denied.Result.IsError || denied.codes() != `["RULE_DENY"]` {
		t.Fatalf("the refund of ord-2 answered %+v, want a block by RULE_DENY", denied)
	}
	rq.refund = denied.meta("request_id")
	update := ra.timed(t, "update_order", map[string]string{"id": "ord-2", "status": "refunded"})
	if update.Result.IsError || update.text() != "order ord-2 is now refunded" {
		t.Fatalf("the update of ord-2 answered %+v, want it run", update)
	}
	rq.update = update.meta("request_id")
	return rq
}

// TestARetriedRefundIsStoppedUnderProcedure02 runs the retried refund on a
// live plane and supervises the open run against the 0.2 procedure: the
// denied refund written as an update of the same order is a confirmed retry,
// the orders the binding saw are more than one, and the approved export the
// procedure does not name is an exception taken, not a step outside it. The
// view is drawn and the findings exported. react stops the run on the retry
// alone, and the second run's call runs. The example's own cases pass.
func TestARetriedRefundIsStoppedUnderProcedure02(t *testing.T) {
	tr := newTree(t)
	collector := start(t, tr.dir, environ(), tr.gateway, "collect", "--listen", "127.0.0.1:0", "--out", "trail/plane.jsonl")
	tr.sign(t, collector.after(t, "collect: listening on "))
	tr.makeRoute(t)
	first, second := tr.openRun(t), tr.openRun(t)
	plane := start(t, tr.dir, append(environ(), journalVariable+"="+tr.path("journal")), tr.gateway, "run", "--config", "plane.yaml")
	agents := "http://" + plane.after(t, "listening for agents on ")
	ra := newAgent(t, agents, first.token)
	rq := ra.retryWork(t, tr)
	tr.waitForTrails(t, 4)
	writeFile(t, filepath.Join(tr.root, "plane.export.jsonl"), must(t, tr.dir, tr.gateway, "trail", "export", "trail/plane.jsonl"))

	view := filepath.Join(tr.root, "run.html")
	s := tr.superviseUnder(t, procedure02, "retried", first.id, ra, func(span) string { return first.id }, "--view", view)
	s.expect(t, 1, "DENIED_ACTION_RETRIED_RESOURCE FINDING_VERDICT_CONFIRMED ESCALATION_ALERT",
		"EXCEPTION_TAKEN FINDING_VERDICT_CONFIRMED ESCALATION_INFORM",
		"RESOURCE_OUTSIDE_RUN FINDING_VERDICT_CONFIRMED ESCALATION_ALERT")
	s.printed(t, "observations: 4 taken", "children: inherit, every run of the tree judged", "tree: "+first.id,
		"step fetch-order: requests "+rq.read, "step refund: requests "+rq.refund,
		"rule REPEATED_DENIAL checked", "rule STEP_OUTSIDE_PROCEDURE checked", "rule DEADLINE_EXCEEDED checked",
		"rule REQUIRED_STEP_SKIPPED not checked: the run is open", "rule STEP_OUT_OF_ORDER not checked: the run is open",
		"rule CONTINUED_AFTER_FAILURE not checked: the run is open", "rule RESOURCE_OUTSIDE_RUN checked",
		"rule DENIED_ACTION_RETRIED_ARGUMENTS checked", "rule DENIED_ACTION_RETRIED_RESOURCE checked",
		"rule DENIED_ACTION_RETRIED_AROUND checked", "rule EXCEPTION_TAKEN checked")
	for rule, want := range map[string][]string{
		"DENIED_ACTION_RETRIED_RESOURCE": {rq.refund, rq.update},
		"RESOURCE_OUTSIDE_RUN":           {rq.read, rq.refund, rq.update},
		"EXCEPTION_TAKEN":                {rq.export},
	} {
		f := s.finding(rule)
		var cited []string
		for _, r := range f.GetRefs() {
			cited = append(cited, r.GetEvent().GetRequestId())
		}
		slices.Sort(cited)
		cited = slices.Compact(cited)
		slices.Sort(want)
		if !slices.Equal(cited, want) || f.GetFinding().GetRunId() != first.id {
			t.Errorf("%s names run %s and cites requests %q, want run %s and %q", rule, f.GetFinding().GetRunId(), cited, first.id, want)
		}
	}
	expectPage(t, view, s)
	expectExport(t, tr, s)

	pl := livePlane{health: plane.after(t, "answering /healthz, /metrics and /brand on "), first: ra, second: newAgent(t, agents, second.token)}
	refused := tr.stop(t, pl, first, s, "DENIED_ACTION_RETRIED_RESOURCE", 2)
	tr.waitForTrails(t, 6)
	plane.interrupt(t)
	collector.interrupt(t)
	own := `{"call":"read_order","args":{"id":"ord-1"}}` + "\n"
	if got, want := readFile(t, tr.path("journal", "orders.jsonl")), own+`{"call":"export_orders","args":{"id":"cus-1"}}`+"\n"+
		`{"call":"update_order","args":{"id":"ord-2","status":"refunded"}}`+"\n"+own; got != want {
		t.Fatalf("the orders server received %q, want the customer's order, the approved export, the update and "+
			"the second run's read, never the denied refund or the stopped call", got)
	}
	tr.expectStoppedTrail(t, refused, first.id)

	if out := must(t, tr.dir, tr.control, "procedure", "test", casesFile); out != "ok   the refund retried as an update: RESOURCE_OUTSIDE_RUN CONFIRMED, DENIED_ACTION_RETRIED_RESOURCE CONFIRMED\n"+
		"ok   the export approved: EXCEPTION_TAKEN CONFIRMED\n"+
		"ok   the export without an approval: STEP_OUTSIDE_PROCEDURE CONFIRMED\n"+
		"procedure refund version 2: 3 cases, 3 passed, 0 failed\n" {
		t.Fatalf("procedure test printed %q", out)
	}
}

// expectPage requires the view to be an owner-only page that runs no script,
// says so in its policy, and names each finding.
func expectPage(t *testing.T, path string, s supervision) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("supervise --view wrote no page: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the page has mode %v, want 0600", info.Mode().Perm())
	}
	page := strings.ToLower(readFile(t, path))
	if strings.Contains(page, "<script") || !strings.Contains(page, `http-equiv="content-security-policy"`) {
		t.Errorf("the page holds a script or no content security policy:\n%s", page)
	}
	for _, f := range s.findings {
		if id := f.GetFinding().GetFindingId(); !strings.Contains(page, id) {
			t.Errorf("the page does not name finding %s", id)
		}
	}
}

// exportTrailer is the members of an export's trailer this test reads.
type exportTrailer struct {
	NextCursor string `json:"next_cursor"`
	EndReached bool   `json:"end_reached"`
	TailBytes  int64  `json:"tail_bytes"`
	Identity   string `json:"identity"`
}

// expectExport requires findings export to write the log whole: its header,
// each finding in log order, the report that closed the write, and a trailer
// that reached the end with nothing left open and a cursor to resume from.
func expectExport(t *testing.T, tr tree, s supervision) {
	t.Helper()
	types, ids, trailer := readExport(t, must(t, tr.dir, tr.control, "findings", "export", "--findings", s.dir))
	var want []string
	for _, f := range s.findings {
		want = append(want, f.GetFinding().GetFindingId())
	}
	if got := strings.Join(types, " "); got != "header finding_record finding_record finding_record supervise_report trailer" ||
		!slices.Equal(ids, want) || !trailer.EndReached || trailer.TailBytes != 0 || trailer.Identity != "log_id" || trailer.NextCursor == "" {
		t.Errorf("findings export wrote %s with findings %q and trailer %+v, want the three findings %q whole", got, ids, trailer, want)
	}
}

// readExport is the type of each line of a findings export, the id of each
// finding it holds and its trailer; a header of another format fails t.
func readExport(t *testing.T, out string) ([]string, []string, exportTrailer) {
	t.Helper()
	var types, ids []string
	var trailer exportTrailer
	lines := bufio.NewScanner(strings.NewReader(out))
	for lines.Scan() {
		var line struct {
			Type          string          `json:"type"`
			Format        string          `json:"format"`
			Version       string          `json:"version"`
			FindingRecord json.RawMessage `json:"finding_record"`
		}
		if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
			t.Fatalf("the export line %q: %v", lines.Text(), err)
		}
		types = append(types, line.Type)
		switch line.Type {
		case "header":
			if line.Format != findinglog.ExportFormat || line.Version != findinglog.ExportVersion {
				t.Errorf("the export's header names %s %s", line.Format, line.Version)
			}
		case "finding_record":
			r := &findingv1alpha1.Record{}
			if err := protojson.Unmarshal(line.FindingRecord, r); err != nil || r.GetFindingRecord() == nil {
				t.Fatalf("the export's finding_record %s is no finding record: %v", line.FindingRecord, err)
			}
			ids = append(ids, r.GetFindingRecord().GetFinding().GetFindingId())
		case "trailer":
			if err := json.Unmarshal(lines.Bytes(), &trailer); err != nil {
				t.Fatal(err)
			}
		}
	}
	return types, ids, trailer
}
