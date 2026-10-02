package gateway_test

import (
	"context"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// TestAnAgentsCancelNeverDropsTheClosingRecord: the context a closing comes
// with may already be done, an agent having cancelled the call once it was
// handed out. The call ran or was aborted all the same, so its trail is
// closed, nothing is counted as a sink failure and material calls are not
// halted.
func TestAnAgentsCancelNeverDropsTheClosingRecord(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, closeIt := range map[string]func(*harness) error{
		"closed with the bytes sent": func(h *harness) error {
			d := h.admit(writeEnvelope(), []byte(`{"k":1}`))
			return h.p.Close(cancelled, d, []byte(`{"k":1}`), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS))
		},
		"closed with nothing sent": func(h *harness) error {
			d := h.admit(writeEnvelope(), []byte(`{"k":1}`))
			return h.p.Close(cancelled, d, nil, nil)
		},
		"aborted": func(h *harness) error {
			d := h.admit(writeEnvelope(), []byte(`{"k":1}`))
			return h.p.Abort(cancelled, d, gateway.AbortUntranslatable)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := build(t, modeEnforce, snapshot(t, allowWrites))
			_ = closeIt(h)
			events := h.events()
			if last := events[len(events)-1].GetKind(); last != kindCompleted && last != kindFailed {
				t.Fatalf("the trail ends at %s", last)
			}
			if err := evidence.ValidateChain(events); err != nil {
				t.Errorf("ValidateChain: %v", err)
			}
			if s := h.p.Stats(); s.SinkFailuresAfterEffect != 0 || s.Halted {
				t.Errorf("stats after a cancelled closing: %+v", s)
			}
		})
	}
}
