package supervise_test

import (
	"maps"
	"testing"
	"time"

	"github.com/guardana/control/internal/supervise"
)

func TestTheReportCountsThePlanesOwnBlocksAndNoDenial(t *testing.T) {
	calls := []call{
		{req: "r1", tool: "get_order", upstream: "shop"},
		{req: "x1", tool: "issue_refund", upstream: "pay", at: 10 * time.Second, outcome: "pause"},
		{req: "x2", tool: "issue_refund", upstream: "pay", at: 20 * time.Second, outcome: "pause"},
		{req: "d1", tool: "issue_refund", upstream: "pay", at: 30 * time.Second, outcome: "deny"},
	}
	res := evaluate(t, supervise.Input{Procedure: procWith(t), Exports: []supervise.Export{export(calls...)}})
	if got, want := res.Report.GetRead().GetPlaneBlocks(), map[string]uint64{"PAUSED": 2}; !maps.Equal(got, want) {
		t.Fatalf("report's plane blocks %v, want %v", got, want)
	}
}
