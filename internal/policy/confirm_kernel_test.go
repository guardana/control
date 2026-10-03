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

// TestAClockBehindTheFloorStopsAnUnconfirmedSnapshotsRead: a bundle started
// unconfirmed over a floor whose latest issuedAt is T is held to T. At T less
// a second the kernel stops a read as a cause in the request, which fail-open
// reads never relieve; at T the same read is a stale bundle's, which they do.
func TestAClockBehindTheFloorStopsAnUnconfirmedSnapshotsRead(t *testing.T) {
	t.Parallel()
	d := valid().withDoc(readsAndWrites)
	d.version = "v7"
	b := d.build()
	h := floorHolder(t, newMemStore(floorOf(t, 7, digestOf([]byte(readsAndWrites)), "11:00:00", "12:00:00")))
	if err := h.InstallUnconfirmed(context.Background(), b, pinned()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		now    time.Time
		codes  []string
		action core.EnforcementAction
	}{
		{utc("11:59:59"), []string{"POLICY_STALE"}, core.Block},
		{utc("12:00:00"), []string{"POLICY_STALE", "RULE_ALLOW", "FAIL_OPEN_READ_CONFIGURED"}, core.Execute},
	}
	for _, c := range cases {
		k, err := core.New(core.Options{Mode: controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, MaxStale: 10 * time.Minute, FailOpenRead: true},
			func() time.Time { return c.now }, func() string { return "decision-1" })
		if err != nil {
			t.Fatal(err)
		}
		out := decideWith(k, h, controlv1.EffectClass_EFFECT_CLASS_READ)
		got := out.Decision
		if got.GetVerdict() != controlv1.Verdict_VERDICT_INDETERMINATE || out.Action != c.action || !slices.Equal(got.GetReasonCodes(), c.codes) {
			t.Errorf("at %v: %s %v %q; want INDETERMINATE, %v, %q", c.now, got.GetVerdict(), out.Action, got.GetReasonCodes(), c.action, c.codes)
		}
	}
}

// TestAReadingBehindTheVerifiedTimeIsStaleEvenRefused: a snapshot confirmed
// at 11:28 over a floor whose latest issuedAt is 11:30 is not before 11:30.
// At 11:29 a call stops at the kernel's clock step, and a request refused
// before that step still reads STALE although its age is within the budget;
// at 11:30 the refused request reads FRESH.
func TestAReadingBehindTheVerifiedTimeIsStaleEvenRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := valid().withDoc(readsAndWrites)
	d.version = "v7"
	digest := digestOf([]byte(readsAndWrites))
	h := floorHolder(t, newMemStore(floorOf(t, 7, digest, "11:00:00", "11:30:00")))
	if err := h.InstallUnconfirmed(ctx, d.build(), pinned()); err != nil {
		t.Fatal(err)
	}
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, digest, "11:28:00"), utc("12:00:00")); err != nil {
		t.Fatal(err)
	}
	kernelAt := func(clock string) *core.Kernel {
		k, err := core.New(core.Options{Mode: controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, MaxStale: 10 * time.Minute, FailOpenRead: true},
			func() time.Time { return utc(clock) }, func() string { return "decision-1" })
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	out := decideWith(kernelAt("11:29:00"), h, controlv1.EffectClass_EFFECT_CLASS_READ)
	if out.Action != core.Block || !slices.Equal(out.Decision.GetReasonCodes(), []string{"POLICY_STALE"}) {
		t.Errorf("a read at 11:29: %v %q; want Block and POLICY_STALE alone", out.Action, out.Decision.GetReasonCodes())
	}
	for clock, want := range map[string]controlv1.PolicyFreshness{
		"11:29:00": controlv1.PolicyFreshness_POLICY_FRESHNESS_STALE,
		"11:30:00": controlv1.PolicyFreshness_POLICY_FRESHNESS_FRESH,
	} {
		req := core.Request{Envelope: kernelEnvelope(controlv1.EffectClass_EFFECT_CLASS_READ), Refusal: contract.ErrTooLarge}
		if got := kernelAt(clock).Decide(ctx, req, h.Current()).Decision.GetPolicyFreshness(); got != want {
			t.Errorf("a refused request at %s: freshness %s, want %s", clock, got, want)
		}
	}
}
