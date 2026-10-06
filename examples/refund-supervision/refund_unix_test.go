//go:build unix

package refundsupervision

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/findinglog"
	"google.golang.org/protobuf/encoding/protojson"
)

// journalVariable names the directory the orders server journals each call
// it received into; plane.yaml passes it to the server.
const journalVariable = "VICTIM_JOURNAL_DIR"

// requests are the plane's request ids of the agent's calls, as its answers
// named them.
type requests struct {
	reads, denied []string
	refund        string
}

// timed makes one call under a span of its own.
func (ra *refundAgent) timed(t *testing.T, tool string, args map[string]string) answer {
	t.Helper()
	id, start := newSpan(len(ra.spans)+1), time.Now()
	a := ra.call(t, id, tool, args)
	ra.spans = append(ra.spans, span{id: id, tool: tool, start: start, end: time.Now(), failed: a.Result.IsError})
	return a
}

// work is the refund task: the customer's own order, four orders of other
// customers, each denied, the refund, held, approved and retried under the
// same span, and an upload to an outside host that never passes the plane.
func (ra *refundAgent) work(t *testing.T, tr tree) requests {
	t.Helper()
	var rq requests
	own := ra.timed(t, "read_order", map[string]string{"id": "ord-1"})
	if own.Result.IsError || !strings.HasPrefix(own.text(), "order ord-1: ") {
		t.Fatalf("the read of the customer's order answered %+v", own)
	}
	rq.reads = append(rq.reads, own.meta("request_id"))
	for _, order := range []string{"ord-2", "ord-3", "ord-4", "ord-5"} {
		a := ra.timed(t, "read_order", map[string]string{"id": order})
		if !a.Result.IsError || a.codes() != `["RULE_DENY"]` {
			t.Fatalf("the read of %s answered %+v, want a block by RULE_DENY", order, a)
		}
		rq.reads, rq.denied = append(rq.reads, a.meta("request_id")), append(rq.denied, a.meta("request_id"))
	}
	id, start, args := newSpan(len(ra.spans)+1), time.Now(), map[string]string{"id": "ord-1", "amount": "12.00"}
	held := ra.call(t, id, "refund", args)
	if held.meta("answer") != "pending" || held.meta("approval_id") == "" || held.meta("request_id") == "" {
		t.Fatalf("the refund answered %+v, want pending with an approval id", held)
	}
	must(t, tr.dir, tr.control, "approvals", "approve", "--approver-id", "supervisor", "state/approvals", held.meta("approval_id"))
	done := ra.call(t, id, "refund", args)
	if done.Result.IsError || done.text() != "refunded 12.00 on order ord-1" || done.meta("request_id") != held.meta("request_id") {
		t.Fatalf("the approved retry answered %+v, want the refund on request %s", done, held.meta("request_id"))
	}
	rq.refund = held.meta("request_id")
	ra.spans = append(ra.spans, span{id: id, tool: "refund", start: start, end: time.Now()})
	start = time.Now()
	ra.spans = append(ra.spans, span{id: newSpan(len(ra.spans) + 1), tool: "upload_file", server: "files.example.net",
		start: start, end: start.Add(40 * time.Millisecond)})
	return rq
}

// supervision is one run of supervise and the findings its log holds.
type supervision struct {
	code     int
	out      string
	dir      string
	findings []*findingv1alpha1.FindingRecord
}

// supervise imports the agent's spans, each claiming the run runOf names,
// into an observation log of its own, supervises runID against the procedure
// with the plane's export and that log into a findings log of its own, and
// reads the findings back from the log.
func (tr tree) supervise(t *testing.T, name, runID string, ra *refundAgent, runOf func(span) string) supervision {
	t.Helper()
	dir := filepath.Join(tr.root, name)
	for _, d := range []string{dir, filepath.Join(dir, "observations"), filepath.Join(dir, "findings")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	spans := filepath.Join(dir, "spans.jsonl")
	writeFile(t, spans, otlpSpans(t, ra.trace, "refund-agent", "app.run_id", ra.spans, runOf))
	must(t, tr.dir, tr.control, "observe", "import", "--source", "runtime.source.json", "--log", filepath.Join(dir, "observations"), spans)
	s := supervision{dir: filepath.Join(dir, "findings")}
	s.code, s.out = run(t, tr.dir, tr.control, "supervise", "--procedure", "refund.procedure.json", "--runs", "state/runs",
		"--run", runID, "--findings", s.dir, "--evidence", filepath.Join(tr.root, "plane.export.jsonl"),
		"--source", "runtime.source.json", "--log", filepath.Join(dir, "observations"))
	records, err := findinglog.ReadFile(filepath.Join(s.dir, findinglog.FileName))
	if err != nil {
		t.Fatalf("%s: reading the findings log: %v\n%s", name, err, s.out)
	}
	for _, r := range records {
		if f := r.GetFindingRecord(); f != nil {
			s.findings = append(s.findings, f)
		}
	}
	return s
}

// expect requires exit status code and exactly the findings want, each its
// rule, verdict and escalation, in any order.
func (s supervision) expect(t *testing.T, code int, want ...string) {
	t.Helper()
	var got []string
	for _, f := range s.findings {
		got = append(got, f.GetFinding().GetRuleId()+" "+f.GetFinding().GetVerdict().String()+" "+f.GetEscalation().String())
	}
	slices.Sort(got)
	if s.code != code || !slices.Equal(got, want) {
		t.Fatalf("supervise exited %d with findings %q, want %d and %q:\n%s", s.code, got, code, want, s.out)
	}
}

func (s supervision) finding(rule string) *findingv1alpha1.FindingRecord {
	for _, f := range s.findings {
		if f.GetFinding().GetRuleId() == rule {
			return f
		}
	}
	return nil
}

func (s supervision) printed(t *testing.T, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains("\n"+s.out, "\n"+line+"\n") {
			t.Errorf("supervise did not print the line %q:\n%s", line, s.out)
		}
	}
}

// TestARefundRunIsSupervisedAgainstItsProcedure runs the refund task on a
// live plane under an opened run, then supervises the run, still open, from
// the plane's trail and the runtime's own spans: the four denials are
// confirmed, the upload the plane never saw is suspected, and nothing else is
// found; the rules about what is missing wait for the run to close. react
// turns the confirmed finding into a stop that refuses the run's next call
// and not a second run's, until a lift ends it. The same upload claiming the
// second run is not the first run's, and the notifier hands each alert to the
// receiver once.
func TestARefundRunIsSupervisedAgainstItsProcedure(t *testing.T) {
	started := time.Now()
	tr := newTree(t)
	collector := start(t, tr.dir, environ(), tr.gateway, "collect", "--listen", "127.0.0.1:0", "--out", "trail/plane.jsonl")
	tr.sign(t, collector.after(t, "collect: listening on "))
	liftKey := tr.makeRoute(t)
	first, second := tr.openRun(t), tr.openRun(t)
	runID := first.id
	plane := start(t, tr.dir, append(environ(), journalVariable+"="+tr.path("journal")), tr.gateway, "run", "--config", "plane.yaml")
	agents := "http://" + plane.after(t, "listening for agents on ")
	ra := newAgent(t, agents, first.token)
	rq := ra.work(t, tr)
	tr.waitForTrails(t, "trail/plane.jsonl", 6)
	writeFile(t, filepath.Join(tr.root, "plane.export.jsonl"), must(t, tr.dir, tr.gateway, "trail", "export", "trail/plane.jsonl"))

	claimed := tr.supervise(t, "claimed", runID, ra, func(span) string { return runID })
	claimed.expect(t, 1, "REPEATED_DENIAL FINDING_VERDICT_CONFIRMED ESCALATION_ALERT",
		"STEP_OUTSIDE_PROCEDURE FINDING_VERDICT_SUSPECTED ESCALATION_ALERT")
	claimed.printed(t, "observations: 7 taken", "step fetch-order: requests "+strings.Join(rq.reads, ", "),
		"step refund: requests "+rq.refund, "rule REPEATED_DENIAL checked", "rule STEP_OUTSIDE_PROCEDURE checked",
		"rule DEADLINE_EXCEEDED checked", "rule REQUIRED_STEP_SKIPPED not checked: the run is open",
		"rule STEP_OUT_OF_ORDER not checked: the run is open", "rule CONTINUED_AFTER_FAILURE not checked: the run is open")
	var cited []string
	for _, r := range claimed.finding("REPEATED_DENIAL").GetRefs() {
		cited = append(cited, r.GetEvent().GetRequestId())
	}
	if !slices.Equal(cited, rq.denied) {
		t.Errorf("REPEATED_DENIAL cites requests %q, want the four denied reads %q", cited, rq.denied)
	}
	outside := claimed.finding("STEP_OUTSIDE_PROCEDURE").GetRefs()
	if len(outside) != 1 || outside[0].GetObservation().GetSourceId() != "refund-agent-runtime" {
		t.Errorf("STEP_OUTSIDE_PROCEDURE cites %v, want the one observation of the runtime", outside)
	}

	pl := livePlane{health: plane.after(t, "answering /healthz, /metrics and /brand on "), first: ra, second: newAgent(t, agents, second.token)}
	refused := tr.stopAndLift(t, pl, first, claimed, liftKey)
	tr.waitForTrails(t, "trail/plane.jsonl", 9)
	plane.interrupt(t)
	collector.interrupt(t)
	own := `{"call":"read_order","args":{"id":"ord-1"}}` + "\n"
	if got, want := readFile(t, tr.path("journal", "orders.jsonl")), own+`{"call":"refund","args":{"amount":"12.00","id":"ord-1"}}`+"\n"+own+own; got != want {
		t.Fatalf("the orders server received %q, want the customer's order, the approved refund, the second run's read "+
			"and the first run's read after the lift, never the stopped call", got)
	}
	tr.expectStoppedTrail(t, refused, runID)

	control := tr.supervise(t, "control", runID, ra, func(s span) string {
		if s.tool == "upload_file" {
			return second.id
		}
		return runID
	})
	control.expect(t, 1, "REPEATED_DENIAL FINDING_VERDICT_CONFIRMED ESCALATION_ALERT")
	control.printed(t, "observations: 6 taken, 1 another run")
	if a, b := control.findings[0].GetFinding().GetFindingId(), claimed.finding("REPEATED_DENIAL").GetFinding().GetFindingId(); a != b {
		t.Errorf("the same denials took finding id %s in one log and %s in another", a, b)
	}

	tr.notifyOnce(t, claimed)
	t.Logf("the run, its supervision and its notification took %v", time.Since(started).Round(time.Millisecond))
}

// notifyOnce hands the findings to a receiver that appends each line it is
// given to a file: the first notify delivers each alert once, in log order,
// and a second delivers nothing.
func (tr tree) notifyOnce(t *testing.T, s supervision) {
	t.Helper()
	state, received := filepath.Join(tr.root, "notify"), filepath.Join(tr.root, "received.jsonl")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	notify := func(init ...string) string {
		args := append(append([]string{"notify"}, init...), "--findings", s.dir, "--state", state, "--", "/bin/sh", "-c", `cat >> "$0"`, received)
		return must(t, tr.dir, tr.control, args...)
	}
	if out := notify("--init"); out != "2 delivered, 0 already delivered, 0 failed, 0 inform left\n" {
		t.Fatalf("the first notify printed %q", out)
	}
	if out := notify(); out != "0 delivered, 2 already delivered, 0 failed, 0 inform left\n" {
		t.Fatalf("the second notify printed %q", out)
	}
	var got []string
	lines := bufio.NewScanner(strings.NewReader(readFile(t, received)))
	for lines.Scan() {
		r := &findingv1alpha1.Record{}
		if err := protojson.Unmarshal(lines.Bytes(), r); err != nil || r.GetFindingRecord() == nil {
			t.Fatalf("the receiver was given %q, which is no finding record: %v", lines.Text(), err)
		}
		got = append(got, r.GetFindingRecord().GetFinding().GetFindingId())
	}
	want := []string{s.findings[0].GetFinding().GetFindingId(), s.findings[1].GetFinding().GetFindingId()}
	if !slices.Equal(got, want) {
		t.Fatalf("the receiver was given the findings %q, want each alert once in log order, %q", got, want)
	}
}
