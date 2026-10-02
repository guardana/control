package policy_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/pkg/contract"
)

// readsAndWrites allows a READ and a WRITE, so only freshness can keep either
// from running.
const readsAndWrites = `{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":"payments","maxStaleSeconds":300,"serial":7,"version":"v7"},` +
	`"rules":[{"effect":"ALLOW","id":"reads-and-writes","when":{"action":{"effect":["READ","WRITE"]}}}]}`

func kernelEnvelope(effect controlv1.EffectClass) *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     "req-1",
		ProjectId:     "proj-1",
		TenantId:      "tenant-1",
		Environment:   "prod",
		OccurredAt:    timestamppb.New(time.Date(2026, time.September, 11, 11, 59, 0, 0, time.UTC)),
		Principal:     &controlv1.Principal{Id: "user-1", TenantId: "tenant-1"},
		Agent:         &controlv1.Agent{Id: "agent-1"},
		Action:        &controlv1.Action{Name: "orders.touch", Effect: effect, Provider: "orders"},
		Resource:      &controlv1.Resource{Type: "order", Id: "ord-1", TenantId: "tenant-1", Environment: "prod"},
	}
}

// TestTheKernelDecidesAnUnconfirmedSnapshotStale goes through the kernel's
// public API: an unconfirmed snapshot's decisions are INDETERMINATE with
// POLICY_STALE and the zero time as policy_loaded_at, so a read and a write
// alike are blocked; a statement confirms it at its issuedAt, and Unconfirm
// makes it stale again.
func TestTheKernelDecidesAnUnconfirmedSnapshotStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := valid().withDoc(readsAndWrites)
	d.version = "v7"
	b := d.build()
	h := floorHolder(t, newMemStore(emptyFloor(t, "payments")))
	if err := h.InstallUnconfirmed(ctx, b, pinned()); err != nil {
		t.Fatal(err)
	}
	k, err := core.New(core.Options{Mode: controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, MaxStale: 10 * time.Minute},
		func() time.Time { return utc("12:01:00") }, func() string { return "decision-1" })
	if err != nil {
		t.Fatal(err)
	}
	expectStale(t, k, h, "unconfirmed")

	if err := h.Confirm(ctx, statementFor(t, "payments", 7, digestOf([]byte(readsAndWrites)), "12:00:00"), utc("12:01:00")); err != nil {
		t.Fatal(err)
	}
	for _, effect := range kernelEffects() {
		out := decideWith(k, h, effect)
		got := out.Decision
		if got.GetVerdict() != controlv1.Verdict_VERDICT_ALLOW || out.Action != core.Execute ||
			got.GetPolicyFreshness() != controlv1.PolicyFreshness_POLICY_FRESHNESS_FRESH ||
			!got.GetPolicyLoadedAt().AsTime().Equal(utc("12:00:00")) {
			t.Fatalf("confirmed, %s: %s %v %s loaded %v; want ALLOW, Execute, FRESH, 12:00:00",
				effect, got.GetVerdict(), out.Action, got.GetPolicyFreshness(), got.GetPolicyLoadedAt().AsTime())
		}
	}

	h.Unconfirm()
	expectStale(t, k, h, "unconfirmed again")
}

func kernelEffects() []controlv1.EffectClass {
	return []controlv1.EffectClass{controlv1.EffectClass_EFFECT_CLASS_READ, controlv1.EffectClass_EFFECT_CLASS_WRITE}
}

func decideWith(k *core.Kernel, h *policy.Holder, effect controlv1.EffectClass) core.Outcome {
	req := core.Request{Envelope: kernelEnvelope(effect), Flow: contract.NewFlowState(false, controlv1.Sensitivity_SENSITIVITY_PUBLIC)}
	return k.Decide(context.Background(), req, h.Current())
}

// expectStale fails unless a read and a write against h's snapshot are each
// INDETERMINATE with POLICY_STALE, blocked, and carry the zero time as
// policy_loaded_at.
func expectStale(t *testing.T, k *core.Kernel, h *policy.Holder, when string) {
	t.Helper()
	for _, effect := range kernelEffects() {
		out := decideWith(k, h, effect)
		got := out.Decision
		if got.GetVerdict() != controlv1.Verdict_VERDICT_INDETERMINATE || out.Action != core.Block ||
			!slices.Equal(got.GetReasonCodes(), []string{"POLICY_STALE", "RULE_ALLOW"}) ||
			got.GetPolicyFreshness() != controlv1.PolicyFreshness_POLICY_FRESHNESS_STALE {
			t.Fatalf("%s, %s: %s %v %q %s; want INDETERMINATE, Block, [POLICY_STALE RULE_ALLOW], STALE",
				when, effect, got.GetVerdict(), out.Action, got.GetReasonCodes(), got.GetPolicyFreshness())
		}
		if loaded := got.GetPolicyLoadedAt(); loaded.GetSeconds() != -62135596800 || loaded.GetNanos() != 0 {
			t.Fatalf("%s, %s: policy_loaded_at %v, want the zero time", when, effect, loaded)
		}
	}
}
