package supervise

import (
	"testing"

	"github.com/guardana/control/internal/pause"
)

func TestActionKindToolIsTheEnvelopes(t *testing.T) {
	if actionKindTool != pause.ActionTool {
		t.Fatalf("a tool call's kind is %q here and %q in the envelope", actionKindTool, pause.ActionTool)
	}
}
