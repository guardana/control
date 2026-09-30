package gateway_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// captureMarker stands for content an adapter put in a preview.
const captureMarker = "planted-content-4b1e"

// encodingSink keeps every event it takes as the spool's line encoding, so a
// test reads the bytes a store would hold rather than the messages.
type encodingSink struct {
	mu     sync.Mutex
	lines  bytes.Buffer
	events evidence.MemorySink
}

func (s *encodingSink) Append(ctx context.Context, e *controlv1.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := evidence.EncodeJSONL(&s.lines, []*controlv1.Event{e}); err != nil {
		return err
	}
	return s.events.Append(ctx, e)
}

func (s *encodingSink) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.lines.Bytes())
}

// previewed is env with a preview of args and a profile, both holding the
// marker, as an adapter that captured content would hand it over.
func previewed(t *testing.T, env *controlv1.ActionEnvelope, args []byte) *controlv1.ActionEnvelope {
	t.Helper()
	env.Arguments = &controlv1.Arguments{
		CanonicalHash:    argumentsHash(t, args),
		RedactedPreview:  `{"note": "` + captureMarker + `"}`,
		RedactionProfile: "profile-" + captureMarker,
	}
	return env
}

// TestNoPreviewReachesTheSink: with no capture setting, the previews an
// adapter hands the plane, on the proposed envelope and on the result, and
// the profiles naming them, are recorded nowhere; the rest of the record is
// kept.
func TestNoPreviewReachesTheSink(t *testing.T) {
	sink := &encodingSink{}
	h := build(t, modeEnforce, snapshot(t, allowWrites, approveRefunds), func(c *gateway.Config) { c.Sink = sink })

	args := []byte(`{"id": 7}`)
	env := previewed(t, writeEnvelope(), args)
	d := h.p.Admit(context.Background(), admission(env, args))
	if d.Action != core.Execute {
		t.Fatalf("Action = %d, want Execute", d.Action)
	}
	const hash = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	res := &controlv1.ActionResult{
		SchemaVersion:          "1.0",
		Status:                 controlv1.ResultStatus_RESULT_STATUS_SUCCESS,
		ToolProtocolStatus:     "ok",
		ResultHash:             hash,
		SideEffectConfirmation: "receipt-9",
		RedactedResultPreview:  "the tool said " + captureMarker,
		RedactionProfile:       "profile-" + captureMarker,
	}
	if err := h.p.Close(context.Background(), d, d.AuthorizedArgs, res); err != nil {
		t.Fatalf("Close: %v", err)
	}

	refund := previewed(t, refundEnvelope(t, refundArgs()), refundArgs())
	refund.RequestId = "req-2"
	if held := h.p.Admit(context.Background(), admission(refund, refundArgs())); held.Pending == nil {
		t.Fatalf("the refund was not held: %+v", held)
	}

	lines := sink.bytes()
	if bytes.Contains(lines, []byte(captureMarker)) {
		t.Errorf("the sink holds the planted content:\n%s", lines)
	}
	expectRecordKept(t, sink.events.Events(), hash)
}

// expectRecordKept asserts the trail of TestNoPreviewReachesTheSink holds
// both proposals and the closing result with everything but their previews.
func expectRecordKept(t *testing.T, events []*controlv1.Event, hash string) {
	t.Helper()
	expectKinds(t, kindsOf(events), []controlv1.EventKind{
		kindProposed, kindDecided, kindStarted, kindCompleted,
		kindProposed, kindDecided, kindApprovalRequested,
	})
	proposed := 0
	for _, e := range events {
		if p := e.GetProposed(); p != nil {
			proposed++
			if !strings.HasPrefix(p.GetArguments().GetCanonicalHash(), "sha256:") || p.GetResource().GetId() != "ord-1" {
				t.Errorf("the recorded proposal lost more than its preview: %+v", p)
			}
		}
	}
	if proposed != 2 || len(events) < 4 {
		t.Fatalf("%d proposals in %d events, want 2 in 7", proposed, len(events))
	}
	closed := events[3].GetResult()
	if closed.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_SUCCESS || closed.GetResultHash() != hash ||
		closed.GetSideEffectConfirmation() != "receipt-9" || closed.GetToolProtocolStatus() != "ok" {
		t.Errorf("the recorded result lost more than its preview: %+v", closed)
	}
}
