package gateway_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// idsThen numbers the first n ids and returns value after that.
func idsThen(n int64, value string) func() string {
	var calls atomic.Int64
	return func() string {
		if i := calls.Add(1); i <= n {
			return "id-" + strconv.FormatInt(i, 10)
		}
		return value
	}
}

// TestNewRefusesAnIDSourceThatCannotTellTwoThingsApart: an empty id and the
// same id twice are both refused before anything is built.
func TestNewRefusesAnIDSourceThatCannotTellTwoThingsApart(t *testing.T) {
	for name, source := range map[string]func() string{
		"empty":            func() string { return "" },
		"constant":         func() string { return "id" },
		"empty the second": idsThen(1, ""),
	} {
		cfg := validConfig(t)
		cfg.NewID = source
		if p, err := gateway.New(cfg); p != nil || !errors.Is(err, gateway.ErrIDSource) {
			t.Errorf("%s: New = %v, %v; want ErrIDSource", name, p, err)
		}
	}
	cfg := validConfig(t)
	cfg.NewID = idsThen(2, "id")
	if _, err := gateway.New(cfg); err != nil {
		t.Errorf("two distinct ids at New: %v", err)
	}
}

// TestAnExecutionThePipelineCannotNameIsBlockedBeforeItStarts: an id source
// that goes on repeating after New, or goes empty, gives a second execution
// the handle of an open one, or none; either is blocked before ACTION_STARTED.
func TestAnExecutionThePipelineCannotNameIsBlockedBeforeItStarts(t *testing.T) {
	repeating := build(t, modeEnforce, snapshot(t, allowWrites), func(c *gateway.Config) { c.NewID = idsThen(2, "dup") })
	if d := repeating.admit(writeEnvelope(), []byte(`{}`)); d.Action != core.Execute {
		t.Fatalf("the first write: Action = %d, decision %+v", d.Action, d.Decision)
	}
	second := repeating.admit(requestNamed(writeEnvelope(), "req-2"), []byte(`{}`))
	expectBlock(t, second, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, kindsOf(repeating.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	if s := repeating.p.Stats(); s.Open != 1 || s.Executed != 1 {
		t.Errorf("Stats = %+v; want the first execution alone open", s)
	}

	empty := build(t, modeEnforce, snapshot(t, allowWrites), func(c *gateway.Config) { c.NewID = idsThen(2, "") })
	d := empty.admit(writeEnvelope(), []byte(`{}`))
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	for _, e := range empty.events() {
		if e.GetKind() == kindStarted {
			t.Errorf("ACTION_STARTED was written for an execution with no name")
		}
	}
	if s := empty.p.Stats(); s.Open != 0 || s.Executed != 0 {
		t.Errorf("Stats = %+v", s)
	}

	// The fourth id is the kernel's first decision id: New took two before
	// it, and the run the call belongs to the third.
	var calls atomic.Int64
	unnamed := build(t, modeEnforce, snapshot(t, allowWrites), func(c *gateway.Config) {
		c.NewID = func() string {
			if i := calls.Add(1); i != 4 {
				return "id-" + strconv.FormatInt(i, 10)
			}
			return ""
		}
	})
	d = unnamed.admit(writeEnvelope(), []byte(`{}`))
	if d.Decision.GetDecisionId() != "" && d.Action == core.Execute {
		t.Fatalf("the decision was named %q; the id source's order moved and this case examines nothing", d.Decision.GetDecisionId())
	}
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, unnamed.kinds(), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
}

// TestARequestIDWithAnOpenTrailIsRefusedWritingNothing: while one request
// runs or is held, a second call under its id is blocked and counted and
// writes nothing; once the first concludes the id is free.
func TestARequestIDWithAnOpenTrailIsRefusedWritingNothing(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowReads, allowWrites, approveRefunds))
	running := h.admit(writeEnvelope(), []byte(`{}`))
	before := len(h.events())
	again := h.admit(readEnvelope(), []byte(`{}`))
	expectBlock(t, again, verdictIndeterminate, codeInvalidFieldValue, gateway.PDPType)
	if got := len(h.events()); got != before {
		t.Errorf("the refused call wrote %d event(s)", got-before)
	}
	if h.p.Stats().Blocks[codeInvalidFieldValue] != 1 {
		t.Errorf("Stats().Blocks = %v", h.p.Stats().Blocks)
	}
	if err := h.p.Close(context.Background(), running, []byte(`{}`), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := h.admit(readEnvelope(), []byte(`{}`)); d.Action != core.Execute {
		t.Errorf("after Close the id is free: Action = %d", d.Action)
	}

	held := build(t, modeEnforce, snapshot(t, allowWrites, approveRefunds))
	first := hold(t, held)
	expectBlock(t, held.admit(writeEnvelope(), []byte(`{}`)), verdictIndeterminate, codeInvalidFieldValue, gateway.PDPType)
	// The held request's own retry, under its own id, resumes it.
	if err := held.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if d := held.admit(retry(t, "req-1"), refundArgs()); d.Action != core.Execute {
		t.Errorf("the held request retried under its own id: Action = %d, decision %+v", d.Action, d.Decision)
	}
}

// TestConcurrentCallsUnderOneRequestIDRunOnce: of many calls under one id at
// once, one runs and every other is refused.
func TestConcurrentCallsUnderOneRequestIDRunOnce(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowWrites))
	const calls = 16
	var wg sync.WaitGroup
	results := make([]gateway.Disposition, calls)
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = h.admit(writeEnvelope(), []byte(`{}`))
		}()
	}
	wg.Wait()
	executed := 0
	for _, d := range results {
		if d.Action == core.Execute {
			executed++
		}
	}
	if executed != 1 || h.p.Stats().Blocks[codeInvalidFieldValue] != calls-1 {
		t.Errorf("%d of %d ran, blocks %v; want one", executed, calls, h.p.Stats().Blocks)
	}
	if err := evidenceOf(h, "req-1"); err != nil {
		t.Errorf("ValidateChain over the one trail: %v", err)
	}
}

func evidenceOf(h *harness, requestID string) error {
	return evidence.ValidateChain(h.trailOf(requestID))
}

// idEmptyAt numbers every id but the nth, which is empty.
func idEmptyAt(n int64) func() string {
	var calls atomic.Int64
	return func() string {
		i := calls.Add(1)
		if i == n {
			return ""
		}
		return "id-" + strconv.FormatInt(i, 10)
	}
}

// The id the pipeline mints for the execution of one allowed write: New takes
// two, the call's run the third, the kernel's decision the fourth,
// ACTION_PROPOSED and POLICY_DECIDED the next two.
const executionIDCall = 7

// TestAnExecutionWithNoNameOfItsOwnIsBlocked: the id source answers every id
// of an allowed write but the execution's, and the call is blocked before
// ACTION_STARTED rather than started under no name; every record it did write
// carries its own id, which is what pins the empty one as the execution's.
func TestAnExecutionWithNoNameOfItsOwnIsBlocked(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowWrites), func(c *gateway.Config) { c.NewID = idEmptyAt(executionIDCall) })
	d := h.admit(writeEnvelope(), []byte(`{}`))
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	events := h.events()
	expectKinds(t, kindsOf(events), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	if err := evidence.ValidateChain(events); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if d.Decision.GetDecisionId() == "" {
		t.Errorf("the decision has no id; the id source's order moved and this test examines another guard")
	}
	for i, e := range events {
		if e.GetEventId() == "" {
			t.Errorf("event %d has no id; the id source's order moved and this test examines another guard", i)
		}
	}
	if s := h.p.Stats(); s.Open != 0 || s.Executed != 0 {
		t.Errorf("Stats = %+v", s)
	}
	// The same source with that call outside this admission runs the write.
	other := build(t, modeEnforce, snapshot(t, allowWrites), func(c *gateway.Config) { c.NewID = idEmptyAt(executionIDCall + 100) })
	if run := other.admit(writeEnvelope(), []byte(`{}`)); run.Action != core.Execute || run.ExecutionID == "" {
		t.Errorf("with every id answered: Action = %d, execution %q; want Execute with a name", run.Action, run.ExecutionID)
	}
}

// TestNoMoreThanMaxOpenExecutionsAreHandedOut: with the bound reached a call is
// blocked with EVIDENCE_UNAVAILABLE on its own trail and nothing is started;
// closing one makes room for the next.
func TestNoMoreThanMaxOpenExecutionsAreHandedOut(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowReads), func(c *gateway.Config) { c.MaxOpen = 1 })
	first := h.admit(readEnvelope(), []byte(`{}`))
	if first.Action != core.Execute {
		t.Fatalf("the first read: Action = %d, decision %+v", first.Action, first.Decision)
	}
	second := h.admit(requestNamed(readEnvelope(), "req-2"), []byte(`{}`))
	expectBlock(t, second, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	if err := evidenceOf(h, "req-2"); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if s := h.p.Stats(); s.Open != 1 || s.Executed != 1 {
		t.Errorf("Stats = %+v; want the first execution alone", s)
	}
	if err := h.p.Close(context.Background(), first, []byte(`{}`), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if third := h.admit(requestNamed(readEnvelope(), "req-3"), []byte(`{}`)); third.Action != core.Execute {
		t.Errorf("after closing the first: Action = %d, want Execute", third.Action)
	}
}
