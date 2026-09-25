package e2e_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// spoolBudget and closingBytes bound the log in the cases about the budget:
// small enough that a handful of calls reaches it, and a reserve that any
// closing record fits in.
const (
	spoolBudget  = 32 << 10
	closingBytes = 4 << 10
	// fillBound is how many calls a filling loop may make before the test
	// gives up on the budget being reached at all.
	fillBound = 200
)

// TestCollectorOutageBlocksNoDecision: with the collector refusing every
// request, decisions keep flowing and the spool grows; nothing blocks, and when
// the collector comes back every record of the outage is delivered.
func TestCollectorOutageBlocksNoDecision(t *testing.T) {
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowDeletes}})
	p.collector.refuse(func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) })
	agent := p.connect(t, "agent-a")
	const calls = 12
	for i := range calls {
		allowed(t, agent, toolDelete, map[string]any{"path": "/x" + strconv.Itoa(i)})
	}
	if n := p.victim.count(toolDelete); n != calls {
		t.Fatalf("the upstream ran %d times, want %d: an export outage blocked a call", n, calls)
	}
	if s := p.pipeline.Stats(); s.Executed != calls || len(s.Blocks) != 0 || s.SinkFailuresBeforeEffect != 0 {
		t.Fatalf("an export outage reached the request path: %+v", s)
	}
	expectSpooledAndUnreleased(t, p)

	// The outage cost nothing: once the collector answers, the whole trail of
	// every call arrives.
	p.collector.accept()
	events := p.sink.events()
	if len(events) != 4*calls {
		t.Fatalf("the plane recorded %d events for %d calls", len(events), calls)
	}
	eventually(t, func() bool { return delivered(p, events) })
}

// expectSpooledAndUnreleased checks the outage left the records on disk and
// released none of them.
func expectSpooledAndUnreleased(t *testing.T, p *plane) {
	t.Helper()
	stats, err := p.spool.Stats()
	if err != nil {
		t.Fatalf("spool.Stats: %v", err)
	}
	if stats.Unacknowledged <= 0 || stats.Bytes <= 0 {
		t.Fatalf("the spool holds %+v; the outage should have left every record on disk", stats)
	}
	eventually(t, func() bool { return p.exporter.Stats().Retries > 0 })
	if ex := p.exporter.Stats(); ex.Acknowledged != 0 || ex.Quarantined != 0 {
		t.Fatalf("the exporter released records a collector never accepted: %+v", ex)
	}
}

// delivered reports whether the collector accepted a record for every event.
func delivered(p *plane, events []*controlv1.Event) bool {
	lines := strings.Join(p.collector.delivered(), "\n")
	for _, e := range events {
		if !strings.Contains(lines, e.GetEventId()) {
			return false
		}
	}
	return true
}

// TestFullSpoolBlocksMaterialCallsBeforeTheirEffect: once the budget is reached
// a material call is blocked with EVIDENCE_UNAVAILABLE before its effect, and
// so is a read while no risk setting says otherwise.
func TestFullSpoolBlocksMaterialCallsBeforeTheirEffect(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		spoolBytes: spoolBudget, closingReserve: closingBytes, noExport: true,
	})
	agent := p.connect(t, "agent-a")
	ran := fill(t, p, agent)

	// Every further material call is blocked, and the upstream stays where the
	// budget left it.
	again := call(t, agent, toolDelete, map[string]any{"path": "/again"})
	blockedWith(t, again, codeEvidenceUnavailable)
	if n := p.victim.count(toolDelete); n != ran {
		t.Fatalf("the upstream ran %d times, want %d: a call ran with its evidence unwritten", n, ran)
	}
	// A read is refused too: allow_reads is the one setting that changes this,
	// and it is off here.
	read := call(t, agent, toolRead, map[string]any{"path": "/r"})
	blockedWith(t, read, codeEvidenceUnavailable)
	if n := p.victim.count(toolRead); n != 0 {
		t.Fatalf("a read ran unrecorded with no risk setting: %d calls", n)
	}
	s := p.pipeline.Stats()
	if s.SinkFailuresBeforeEffect < 3 || s.Blocks[codeEvidenceUnavailable] < 3 || s.ReadsUnrecorded != 0 {
		t.Fatalf("stats on a full spool: %+v", s)
	}
	if p.sink.refused() < 3 {
		t.Fatalf("the spool refused %d appends", p.sink.refused())
	}
}

// TestAllowReadsUnrecordedIsTheOneRiskSetting: the risk setting of invariant 5
// lets a read run unrecorded and counted, and a material call still never does.
func TestAllowReadsUnrecordedIsTheOneRiskSetting(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		spoolBytes: spoolBudget, closingReserve: closingBytes, noExport: true, allowReadsUnrecorded: true,
	})
	agent := p.connect(t, "agent-a")
	ran := fill(t, p, agent)

	allowed(t, agent, toolRead, map[string]any{"path": "/r"})
	if n := p.victim.count(toolRead); n != 1 {
		t.Fatalf("the read ran %d times under allow_reads", n)
	}
	if s := p.pipeline.Stats(); s.ReadsUnrecorded != 1 {
		t.Fatalf("an unrecorded read was not counted: %+v", s)
	}
	material := call(t, agent, toolDelete, map[string]any{"path": "/again"})
	blockedWith(t, material, codeEvidenceUnavailable)
	if n := p.victim.count(toolDelete); n != ran {
		t.Fatalf("a material call ran unrecorded under allow_reads: %d calls, want %d", n, ran)
	}
}

// TestAClosingRecordWritesOnAFullSpool: a call already in flight when the
// budget is reached still closes its trail, and no closing append fails. What
// this holds is the end of that promise; the reservation itself, the bytes the
// spool keeps back per open trail, is bounded by the spool's own tests, because
// the room left when the budget refuses a call is larger than a closing record.
func TestAClosingRecordWritesOnAFullSpool(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		spoolBytes: spoolBudget, closingReserve: closingBytes, noExport: true,
	})
	slowAgent := p.connect(t, "agent-slow")
	type answer struct {
		res *sdk.CallToolResult
		err error
	}
	done := make(chan answer, 1)
	go func() {
		res, err := slowAgent.CallTool(ctxT(t), &sdk.CallToolParams{Name: toolSlow, Arguments: map[string]any{"path": "/slow"}})
		done <- answer{res, err}
	}()
	// The call is in the upstream, so its ACTION_STARTED and its reservation
	// are on disk.
	eventually(t, func() bool { return p.victim.count(toolSlow) == 1 })

	filler := p.connect(t, "agent-filler")
	fill(t, p, filler)

	got := <-done
	if got.err != nil || got.res.IsError {
		t.Fatalf("the call in flight when the budget was reached: %v %+v", got.err, got.res)
	}
	_, byRequest := p.trails()
	var closed bool
	for _, trail := range byRequest {
		if trail[0].GetProposed().GetAction().GetName() != toolSlow {
			continue
		}
		expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
		closed = true
	}
	if !closed {
		t.Fatal("no trail of the slow call was recorded at all")
	}
	if s := p.pipeline.Stats(); s.SinkFailuresAfterEffect != 0 {
		t.Errorf("a closing record was refused although its bytes were reserved: %+v", s)
	}
}

// fill makes material calls until the budget refuses one, and returns how many
// of them ran. A bound that is never reached fails the test: a case about a
// full spool that never fills it examines nothing.
func fill(t *testing.T, p *plane, agent *sdk.ClientSession) int {
	t.Helper()
	for i := range fillBound {
		res := call(t, agent, toolDelete, map[string]any{"path": "/fill" + strconv.Itoa(i)})
		if !res.IsError {
			continue
		}
		blockedWith(t, res, codeEvidenceUnavailable)
		if n := p.victim.count(toolDelete); n != i {
			t.Fatalf("the upstream ran %d times before the block, want %d: the blocked call reached it", n, i)
		}
		return i
	}
	t.Fatalf("%d calls did not reach the budget of %d bytes", fillBound, spoolBudget)
	return 0
}
