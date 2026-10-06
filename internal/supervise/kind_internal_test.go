package supervise

import (
	"slices"
	"testing"

	"github.com/guardana/control/internal/pause"
)

func TestActionKindToolIsTheEnvelopes(t *testing.T) {
	if actionKindTool != pause.ActionTool {
		t.Fatalf("a tool call's kind is %q here and %q in the envelope", actionKindTool, pause.ActionTool)
	}
}

func TestRuleIDsIsTheRulesAReportLists(t *testing.T) {
	if got := RuleIDs(); !slices.Equal(got, ruleIDs[:]) {
		t.Fatalf("RuleIDs() = %q, want %q", got, ruleIDs)
	}
}
