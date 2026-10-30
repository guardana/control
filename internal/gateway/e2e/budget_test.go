package e2e_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/segmentlog"
	"github.com/guardana/control/internal/trailfile"
)

// trailBudget bounds the collector's segments in the case about its budget:
// a few calls' trails, well under the spool's own budget.
const (
	trailBudget  = 8 << 10
	trailSegment = trailBudget / 4
)

// startLogCollector is the collector over a directory of trail segments
// bound to the plane's tenant and project, as collect runs it.
func startLogCollector(t *testing.T, cfg trailfile.LogConfig, addr string) (*fileCollector, *trailfile.Log) {
	t.Helper()
	l, err := trailfile.OpenLog(cfg)
	if err != nil {
		t.Fatalf("trailfile.OpenLog: %v", err)
	}
	return serveCollector(t, l, l, addr), l
}

// TestAtTheTrailsBudgetAMaterialCallIsBlocked: a plane exporting to a
// collector whose segments reach their bound has its requests answered 503;
// its spool fills, and a material call is blocked with EVIDENCE_UNAVAILABLE
// without reaching the upstream. Nothing is pruned to make room. Once an
// operator prunes the sealed segments and the collector is back, the spool
// drains and the same call runs.
func TestAtTheTrailsBudgetAMaterialCallIsBlocked(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		spoolBytes: spoolBudget, closingReserve: closingBytes, noExport: true,
	})
	dir := filepath.Join(t.TempDir(), "trail")
	cfg := trailfile.LogConfig{Dir: dir, Tenant: "t1", Project: "p1", SegmentBytes: trailSegment, MaxBytes: trailBudget}
	c, l := startLogCollector(t, cfg, "127.0.0.1:0")
	exporter := exportTo(t, p, c.url())
	agent := p.connect(t, "agent-a")

	fillUntilBlocked(t, p, agent)
	if st := exporter.Stats(); st.RetriedServer == 0 {
		t.Errorf("the collector never answered 503 at its budget: %+v", st)
	}
	m := l.Manifest()
	if m.Retained > trailBudget || len(m.Segments) == 0 {
		t.Fatalf("the trail holds %d bytes in %d sealed segments, bound %d", m.Retained, len(m.Segments), trailBudget)
	}
	for _, s := range m.Segments {
		if s.State != segmentlog.StateSealed {
			t.Errorf("segment %d is %s: something was pruned without an operator", s.Seq, s.State)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if again := l.Manifest(); again.Retained != m.Retained || len(again.Segments) != len(m.Segments) {
		t.Errorf("the full trail changed on its own: %+v, was %+v", again, m)
	}

	c.stop()
	pruner, err := trailfile.OpenLog(trailfile.LogConfig{Dir: dir, Tenant: "t1", Project: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := pruner.Prune(len(m.Segments)); err != nil {
		t.Fatal(err)
	}
	if err := pruner.Close(); err != nil {
		t.Fatal(err)
	}
	startLogCollector(t, trailfile.LogConfig{Dir: dir, Tenant: "t1", Project: "p1", SegmentBytes: trailSegment,
		MaxBytes: 4 * spoolBudget}, c.addr)
	eventually(t, func() bool {
		st, err := p.spool.Stats()
		return err == nil && st.Unacknowledged == 0
	})
	allowed(t, agent, toolDelete, map[string]any{"path": "/after-prune"})
}

// TestACollectorTakesNothingWithoutItsCredential: a plane sending another
// credential is answered 401 every time, the trail stays empty, and the
// plane's spool fills until a material call is blocked.
func TestACollectorTakesNothingWithoutItsCredential(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		spoolBytes: spoolBudget, closingReserve: closingBytes, noExport: true,
	})
	dir := filepath.Join(t.TempDir(), "trail")
	c, l := startLogCollector(t, trailfile.LogConfig{Dir: dir, Tenant: "t1", Project: "p1", SegmentBytes: 1 << 20,
		MaxBytes: 2 << 20}, "127.0.0.1:0")
	exporter := exportWith(t, p, c.url(), "Bearer "+collectorToken[1:]+"x")
	agent := p.connect(t, "agent-a")

	fillUntilBlocked(t, p, agent)
	if st := exporter.Stats(); st.RetriedAuth == 0 || st.Acknowledged != 0 {
		t.Errorf("the exporter reports %+v, want 401s and nothing acknowledged", st)
	}
	if m := l.Manifest(); m.Retained != 0 {
		t.Errorf("a plane without the credential wrote %d bytes", m.Retained)
	}
}

// TestAnotherProjectsEvidenceIsQuarantinedByThePlane: a collector bound to
// another project answers the plane's records 400, the plane quarantines
// them, and the trail holds none of them.
func TestAnotherProjectsEvidenceIsQuarantinedByThePlane(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads}, noExport: true,
	})
	dir := filepath.Join(t.TempDir(), "trail")
	c, l := startLogCollector(t, trailfile.LogConfig{Dir: dir, Tenant: "t1", Project: "p2", SegmentBytes: 1 << 20,
		MaxBytes: 2 << 20}, "127.0.0.1:0")
	exporter := exportTo(t, p, c.url())
	agent := p.connect(t, "agent-a")

	allowed(t, agent, toolRead, map[string]any{"path": "/r"})
	recorded := uint64(len(p.sink.events()))
	eventually(t, func() bool { return exporter.Stats().Quarantined == recorded })
	if m := l.Manifest(); m.Retained != 0 {
		t.Errorf("a collector of another project wrote %d bytes of this plane's", m.Retained)
	}
}
