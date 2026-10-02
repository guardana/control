package gateway_test

import (
	"context"
	"testing"
	"time"

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

// TestAnAgentsCancelNeverStrandsAnotherRequestsExpiredHold: a call reserving
// its trail closes the holds that expired on the way, which are other
// requests'; its own cancellation must not leave their trails open.
func TestAnAgentsCancelNeverStrandsAnotherRequestsExpiredHold(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	hold(t, h)
	h.clock.set(base().Add(time.Hour))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	env := writeEnvelope()
	env.RequestId = "req-9"
	h.p.Admit(cancelled, admission(env, []byte(`{"k":1}`)))
	trail := h.trailOf("req-1")
	if last := trail[len(trail)-1].GetKind(); last != kindBlocked {
		t.Errorf("the expired hold's trail ends at %s", last)
	}
	if s := h.p.Stats(); s.HeldTrailsLeftOpen != 0 {
		t.Errorf("HeldTrailsLeftOpen = %d after another agent's cancelled call", s.HeldTrailsLeftOpen)
	}
}

// TestAnAgentsCancelAfterTheStartNeverDropsTheLapse: an approval that lapses
// while ACTION_STARTED is appended is aborted with a record, and the agent's
// cancellation after the start does not drop it.
func TestAnAgentsCancelAfterTheStartNeverDropsTheLapse(t *testing.T) {
	r := lapsing(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.sink.hook(kindStarted, func() { r.clock.set(base().Add(lapseTTL)) })
	r.sink.hook(kindFailed, cancel)
	r.p.Admit(ctx, admission(retry(t, "req-2"), refundArgs()))
	expectKinds(t, kindsOf(r.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted, kindFailed})
	if s := r.p.Stats(); s.HeldTrailsLeftOpen != 0 {
		t.Errorf("HeldTrailsLeftOpen = %d after a cancel past the start", s.HeldTrailsLeftOpen)
	}
}
