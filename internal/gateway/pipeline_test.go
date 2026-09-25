package gateway_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

func assertUnbuilt(t *testing.T, d gateway.Disposition) {
	t.Helper()
	if d.Action != core.Block {
		t.Errorf("Action = %d, want Block", d.Action)
	}
	if d.Decision.GetVerdict() != verdictIndeterminate {
		t.Errorf("Verdict = %s, want INDETERMINATE", d.Decision.GetVerdict())
	}
	if codes := d.Decision.GetReasonCodes(); !slices.Equal(codes, []string{codePolicyUnavailable}) {
		t.Errorf("ReasonCodes = %v, want [POLICY_UNAVAILABLE]", codes)
	}
	if d.AuthorizedArgs != nil || d.AuthorizedDigest != "" || d.Pending != nil || d.Obligations != nil {
		t.Errorf("a block handed out arguments, a pending state or obligations: %+v", d)
	}
}

// TestUnbuiltPipelinesBlock: the zero value and a nil pipeline answer with the
// decision of a kernel nobody built, never with an allow, close nothing and
// report themselves halted.
func TestUnbuiltPipelinesBlock(t *testing.T) {
	admission := gateway.Admission{Envelope: readEnvelope(), Arguments: []byte("{}")}
	for name, p := range map[string]*gateway.Pipeline{"nil": nil, "zero": new(gateway.Pipeline)} {
		d := p.Admit(context.Background(), admission)
		assertUnbuilt(t, d)
		if err := p.Close(context.Background(), d, []byte("{}"), &controlv1.ActionResult{}); !errors.Is(err, gateway.ErrUnbuilt) {
			t.Errorf("%s: Close = %v, want ErrUnbuilt", name, err)
		}
		if err := p.Abort(context.Background(), d, gateway.AbortArgsMismatch); !errors.Is(err, gateway.ErrUnbuilt) {
			t.Errorf("%s: Abort = %v, want ErrUnbuilt", name, err)
		}
		if !p.Stats().Halted {
			t.Errorf("%s: Stats().Halted = false; a pipeline nobody built takes no material call", name)
		}
	}
}

func TestZeroDispositionBlocks(t *testing.T) {
	var d gateway.Disposition
	if d.Action != core.Block {
		t.Errorf("the zero Disposition's action is %d, want Block", d.Action)
	}
}

func TestNewRefusesEachMisconfiguration(t *testing.T) {
	observing := gateway.Capabilities{ObserveRequest: true, ObserveResult: true}
	cases := []struct {
		name string
		mut  func(*gateway.Config)
		want error
	}{
		{"zero mode", func(c *gateway.Config) { c.Mode = 0 }, gateway.ErrMode},
		{"undeclared mode", func(c *gateway.Config) { c.Mode = 99 }, gateway.ErrMode},
		{"planned mode", func(c *gateway.Config) { c.Mode = controlv1.EnforcementMode_ENFORCEMENT_MODE_SHADOW }, gateway.ErrModePlanned},
		{"nil adapter", func(c *gateway.Config) { c.Adapter = nil }, gateway.ErrNoAdapter},
		{"an adapter that cannot block under ENFORCE", func(c *gateway.Config) {
			c.Adapter = fakeAdapter{name: "observer", caps: observing}
		}, gateway.ErrCapability},
		{"an adapter that cannot block under LOCKDOWN", func(c *gateway.Config) {
			c.Mode = modeLockdown
			c.Adapter = fakeAdapter{name: "observer", caps: observing}
		}, gateway.ErrCapability},
		{"an adapter that sees nothing under OBSERVE", func(c *gateway.Config) {
			c.Mode = modeObserve
			c.Adapter = fakeAdapter{name: "blind"}
		}, gateway.ErrCapability},
		{"an obligation outside the catalogue", func(c *gateway.Config) {
			caps := enforcing
			caps.Obligations = []string{"redact_fields", "teleport"}
			c.Adapter = fakeAdapter{name: "fake", caps: caps}
		}, gateway.ErrObligation},
		{"an end-user binding on an adapter that authenticates nobody", func(c *gateway.Config) {
			caps := enforcing
			caps.BindEndUser = true
			c.Adapter = fakeAdapter{name: "fake", caps: caps}
		}, gateway.ErrUnauthenticatedBinding},
		{"a staleness budget the kernel refuses", func(c *gateway.Config) { c.KernelOptions.MaxStale = 0 }, core.ErrMaxStale},
		{"nil policy", func(c *gateway.Config) { c.Policy = nil }, gateway.ErrNoPolicy},
		{"nil sink", func(c *gateway.Config) { c.Sink = nil }, gateway.ErrNoSink},
		{"nil approvals", func(c *gateway.Config) { c.Approvals = nil }, gateway.ErrNoApprovals},
		{"nil clock", func(c *gateway.Config) { c.Clock = nil }, gateway.ErrNoClock},
		{"nil id source", func(c *gateway.Config) { c.NewID = nil }, gateway.ErrNoIDSource},
		{"a zero approval lifetime", func(c *gateway.Config) { c.ApprovalTTL = 0 }, gateway.ErrApprovalTTL},
		{"a negative approval lifetime", func(c *gateway.Config) { c.ApprovalTTL = -1 }, gateway.ErrApprovalTTL},
		{"a zero retry interval", func(c *gateway.Config) { c.RetryAfter = 0 }, gateway.ErrRetryAfter},
		{"a negative retry interval", func(c *gateway.Config) { c.RetryAfter = -1 }, gateway.ErrRetryAfter},
		{"no bound on held requests", func(c *gateway.Config) { c.MaxHeld = 0 }, gateway.ErrMaxHeld},
		{"a negative bound on held requests", func(c *gateway.Config) { c.MaxHeld = -1 }, gateway.ErrMaxHeld},
		{"no bound on open executions", func(c *gateway.Config) { c.MaxOpen = 0 }, gateway.ErrMaxOpen},
		{"a negative bound on open executions", func(c *gateway.Config) { c.MaxOpen = -1 }, gateway.ErrMaxOpen},
		{"no bound on runs", func(c *gateway.Config) { c.MaxRuns = 0 }, gateway.ErrMaxRuns},
		{"a negative bound on runs", func(c *gateway.Config) { c.MaxRuns = -1 }, gateway.ErrMaxRuns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig(t)
			tc.mut(&cfg)
			p, err := gateway.New(cfg)
			if p != nil || !errors.Is(err, tc.want) {
				t.Errorf("New = %v, %v; want nil, %v", p, err, tc.want)
			}
		})
	}
}

// TestEveryCoversClauseIsHeldAtItsEdge: for each capability the table can
// need, an adapter declaring everything the mode needs but that one is
// refused, so no single clause of covers can be replaced with true.
func TestEveryCoversClauseIsHeldAtItsEdge(t *testing.T) {
	cases := []struct {
		name string
		mode controlv1.EnforcementMode
		caps gateway.Capabilities
	}{
		{"an adapter that sees the answer but not the call under OBSERVE", modeObserve, gateway.Capabilities{ObserveResult: true}},
		{"an adapter that sees the call but not the answer under OBSERVE", modeObserve, gateway.Capabilities{ObserveRequest: true}},
		{"an adapter that sees both and cannot block under ENFORCE", modeEnforce, gateway.Capabilities{ObserveRequest: true, ObserveResult: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig(t)
			cfg.Mode = tc.mode
			cfg.Adapter = fakeAdapter{name: "partial", caps: tc.caps}
			if p, err := gateway.New(cfg); p != nil || !errors.Is(err, gateway.ErrCapability) {
				t.Errorf("New = %v, %v; want nil, ErrCapability", p, err)
			}
		})
	}
	// No row needs Authenticates, BindEndUser, SeeDelegation or
	// SeeResourceIDs, so their clauses have no refusing input through New;
	// this pins that no row asks for them, and the day one does, the table
	// above gets its case.
	values := controlv1.EnforcementMode(0).Descriptor().Values()
	for i := range values.Len() {
		mode := controlv1.EnforcementMode(values.Get(i).Number())
		need, err := gateway.Requirements(mode)
		if errors.Is(err, gateway.ErrMode) {
			continue
		}
		if need.Authenticates || need.BindEndUser || need.SeeDelegation || need.SeeResourceIDs {
			t.Errorf("Requirements(%s) = %+v asks for a capability this test has no refusing case for", mode, need)
		}
	}
}

// TestNewAcceptsWhatTheModeNeeds: the same adapter runs under OBSERVE without
// Block and under the blocking modes with it, a declared obligation in the
// catalogue passes, and an adapter that authenticates may bind an end user.
func TestNewAcceptsWhatTheModeNeeds(t *testing.T) {
	cfg := validConfig(t)
	cfg.Mode = modeObserve
	cfg.Adapter = fakeAdapter{name: "observer", caps: gateway.Capabilities{ObserveRequest: true, ObserveResult: true}}
	if _, err := gateway.New(cfg); err != nil {
		t.Errorf("New under OBSERVE with an observing adapter: %v", err)
	}
	// The kernel's mode and applicable set are the pipeline's to fix: a caller
	// cannot put the kernel in another mode or hand it an obligation type.
	cfg = validConfig(t)
	cfg.KernelOptions.Mode = modeObserve
	cfg.KernelOptions.Applicable = []string{"teleport"}
	if _, err := gateway.New(cfg); err != nil {
		t.Errorf("New overrides the kernel's mode and applicable set; got %v", err)
	}
	cfg = validConfig(t)
	caps := enforcing
	caps.Obligations = []string{"redact_fields"}
	caps.Authenticates, caps.BindEndUser = true, true
	cfg.Adapter = fakeAdapter{name: "fake", caps: caps}
	for _, mode := range []controlv1.EnforcementMode{modeApprove, modeEnforce, modeLockdown} {
		cfg.Mode = mode
		if _, err := gateway.New(cfg); err != nil {
			t.Errorf("New under %s: %v", mode, err)
		}
	}
}
