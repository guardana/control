package gateway_test

import (
	"context"
	"errors"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// TestACallThatWasNotSentIsAbortedNotClosed: Close with no bytes refuses with
// ErrNothingSent and aborts the execution itself, because Admit hands out no
// nil authorized bytes; its record says nothing was sent and no comparison
// ran, nothing is left open, and the plane goes on taking material calls.
func TestACallThatWasNotSentIsAbortedNotClosed(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowWrites))
	d := h.admit(writeEnvelope(), []byte(`{"k":1}`))
	if err := h.p.Close(context.Background(), d, nil, result(controlv1.ResultStatus_RESULT_STATUS_BLOCKED)); !errors.Is(err, gateway.ErrNothingSent) {
		t.Fatalf("Close with nothing sent = %v, want ErrNothingSent", err)
	}
	expectAborted(t, h.events(), d, codeInvalidFieldValue)
	expectConsumedByAbort(t, h, d)
}

// TestAbortClosesWhatTheAdapterDidNotSend: the cause the adapter names is the
// code the closing record carries, one code per cause, and the execution is
// consumed.
func TestAbortClosesWhatTheAdapterDidNotSend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause gateway.AbortCause
		code  string
	}{
		{"the bytes disagreed", gateway.AbortArgsMismatch, codeExecutedArgsMismatch},
		{"an obligation refused", gateway.AbortObligation, codeObligationNotUnderstood},
		{"no upstream serves it", gateway.AbortUnroutable, codeActionUnclassified},
		{"the protocol cannot carry it", gateway.AbortUntranslatable, codeInvalidFieldValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := build(t, modeEnforce, snapshot(t, allowWrites))
			d := h.admit(writeEnvelope(), []byte(`{"k":1}`))
			if s := h.p.Stats(); s.Open != 1 {
				t.Fatalf("Stats = %+v; want the execution open", s)
			}
			if err := h.p.Abort(context.Background(), d, tc.cause); err != nil {
				t.Fatalf("Abort: %v", err)
			}
			expectAborted(t, h.events(), d, tc.code)
			expectConsumedByAbort(t, h, d)
		})
	}
}

// expectAborted asserts the trail ends in the aborted record of d, naming code.
func expectAborted(t *testing.T, events []*controlv1.Event, d gateway.Disposition, code string) {
	t.Helper()
	expectKinds(t, kindsOf(events), []controlv1.EventKind{kindProposed, kindDecided, kindStarted, kindFailed})
	if err := evidence.ValidateChain(events); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	aborted := events[len(events)-1].GetResult()
	if aborted.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_BLOCKED || aborted.GetExecutedActionDigest() != "" ||
		aborted.GetToolProtocolStatus() != code || aborted.GetRequestId() != "req-1" ||
		aborted.GetExecutionId() != d.ExecutionID || aborted.GetSchemaVersion() != "1.0" || aborted.GetEndedAt() == nil {
		t.Errorf("the aborted record = %+v; want %s and no executed digest", aborted, code)
	}
}

// expectConsumedByAbort asserts d can be neither aborted nor closed again,
// and the plane goes on taking material calls.
func expectConsumedByAbort(t *testing.T, h *harness, d gateway.Disposition) {
	t.Helper()
	if err := h.p.Abort(context.Background(), d, gateway.AbortObligation); !errors.Is(err, gateway.ErrNotMinted) {
		t.Errorf("a second Abort = %v, want ErrNotMinted", err)
	}
	if err := h.p.Close(context.Background(), d, []byte(`{"k":1}`), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); !errors.Is(err, gateway.ErrNotMinted) {
		t.Errorf("Close after Abort = %v, want ErrNotMinted", err)
	}
	if err := h.p.Close(context.Background(), d, nil, result(controlv1.ResultStatus_RESULT_STATUS_BLOCKED)); !errors.Is(err, gateway.ErrNotMinted) {
		t.Errorf("Close with nothing sent after Abort = %v, want ErrNotMinted", err)
	}
	if s := h.p.Stats(); s.Open != 0 || s.Halted || s.Mismatches != 0 {
		t.Errorf("after Abort: Stats = %+v", s)
	}
	if next := h.admit(requestNamed(writeEnvelope(), "req-next"), []byte(`{}`)); next.Action != core.Execute {
		t.Errorf("a write after an abort: Action = %d, want Execute", next.Action)
	}
	if err := h.p.Abort(context.Background(), gateway.Disposition{}, gateway.AbortArgsMismatch); !errors.Is(err, gateway.ErrNotMinted) {
		t.Errorf("Abort of the zero disposition = %v, want ErrNotMinted", err)
	}
}

// TestAnAbortTheSinkRefusesIsReturnedAndDoesNotHalt: nothing ran, so the
// refused record is counted before the effect and material calls go on.
func TestAnAbortTheSinkRefusesIsReturnedAndDoesNotHalt(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowWrites))
	d := h.admit(writeEnvelope(), []byte(`{}`))
	h.sink.refuse[kindFailed] = true
	if err := h.p.Abort(context.Background(), d, gateway.AbortArgsMismatch); !errors.Is(err, errSink) {
		t.Fatalf("Abort under a refusing sink = %v, want the sink's error", err)
	}
	if s := h.p.Stats(); s.Halted || s.SinkFailuresAfterEffect != 0 || s.SinkFailuresBeforeEffect != 1 || s.Open != 0 {
		t.Errorf("Stats = %+v", s)
	}
	if next := h.admit(requestNamed(writeEnvelope(), "req-2"), []byte(`{}`)); next.Action != core.Execute {
		t.Errorf("a write after a refused abort record: Action = %d, want Execute", next.Action)
	}
}

// TestAnUnrecordedReadIsAbortedUnrecorded: under the risk setting a read
// whose trail the sink refused writes no closing record either.
func TestAnUnrecordedReadIsAbortedUnrecorded(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowReads), func(c *gateway.Config) { c.AllowReadsUnrecorded = true })
	h.sink.fail(true)
	d := h.admit(readEnvelope(), []byte(`{}`))
	if d.Action != core.Execute {
		t.Fatalf("Action = %d", d.Action)
	}
	h.sink.fail(false)
	if err := h.p.Abort(context.Background(), d, gateway.AbortArgsMismatch); err != nil {
		t.Errorf("Abort of an unrecorded read = %v", err)
	}
	if n := len(h.events()); n != 0 {
		t.Errorf("the sink kept %d event(s) of a trail it refused", n)
	}
}

// TestTheClosingRecordNamesThePipelinesIDs: whatever the adapter put in the
// result's request and execution ids, the record carries the trail's.
func TestTheClosingRecordNamesThePipelinesIDs(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowWrites))
	d := h.admit(writeEnvelope(), []byte(`{}`))
	forged := result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)
	forged.RequestId, forged.ExecutionId = "req-other", "exec-other"
	if err := h.p.Close(context.Background(), d, []byte(`{}`), forged); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events := h.events()
	closing := events[len(events)-1]
	if closing.GetResult().GetRequestId() != "req-1" || closing.GetResult().GetExecutionId() != events[2].GetExecutionId() ||
		closing.GetExecutionId() != events[2].GetExecutionId() {
		t.Errorf("the closing record = %+v; want req-1 and the started execution %q", closing.GetResult(), events[2].GetExecutionId())
	}
	if forged.GetRequestId() != "req-other" {
		t.Errorf("Close wrote into the caller's result")
	}
}
