package core

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestNewDecisionClonesTheEnvelope: the decision reads its own copy, taken
// before any step runs, so a caller writing to the envelope afterwards
// changes nothing a later step reads. Nothing outside the package can see
// which pointer the steps read, which is why this test is internal.
func TestNewDecisionClonesTheEnvelope(t *testing.T) {
	calls := 0
	k := &Kernel{clock: func() time.Time { calls++; return time.Time{} }, newID: func() string { return "" }}
	env := &controlv1.ActionEnvelope{RequestId: "req-1", Action: &controlv1.Action{Name: "orders.read"}}
	d := newDecision(k, Request{Envelope: env}, nil)
	if d.env == env || d.env.GetAction() == env.GetAction() {
		t.Fatal("the decision reads the caller's envelope")
	}
	if !proto.Equal(d.env, env) {
		t.Fatalf("the clone differs: %v", d.env)
	}
	env.RequestId, env.Action.Name = "req-2", "orders.delete"
	if d.env.GetRequestId() != "req-1" || d.env.GetAction().GetName() != "orders.read" {
		t.Errorf("a write to the caller's envelope reached the clone: %v", d.env)
	}
	if calls != 1 {
		t.Errorf("the clock was read %d times on entry, want 1", calls)
	}
	if d := newDecision(k, Request{}, nil); d.env != nil {
		t.Errorf("a nil envelope clones to %v", d.env)
	}
}
