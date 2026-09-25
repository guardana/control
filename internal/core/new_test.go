package core_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
)

// TestNewAcceptsEnforce is the accepting twin of every refusal below: a stub
// that refuses everything passes each of them and fails here.
func TestNewAcceptsEnforce(t *testing.T) {
	k, err := core.New(options(), fixedClock(base()), ids())
	if err != nil {
		t.Fatalf("New(ENFORCE, a clock, an id source) = %v, want a kernel", err)
	}
	if k == nil {
		t.Fatal("New returned neither a kernel nor a refusal")
	}
	out := decide(k, request(readEnvelope()), snapshot(t, allowReads))
	check(t, out, expect{verdict: verdictAllow, action: core.Execute, codes: []string{codeRuleAllow}})
}

// TestNewRefusesEveryModeButEnforce walks every declared mode and two numbers
// no build declares. A declared mode other than ENFORCE is named planned; the
// zero value and an undeclared number are refused without that word, since
// neither is a mode anyone plans.
func TestNewRefusesEveryModeButEnforce(t *testing.T) {
	modes := []controlv1.EnforcementMode{controlv1.EnforcementMode(-1), controlv1.EnforcementMode(4096)}
	values := controlv1.EnforcementMode(0).Descriptor().Values()
	for i := 0; i < values.Len(); i++ {
		modes = append(modes, controlv1.EnforcementMode(values.Get(i).Number()))
	}
	if len(modes) < 9 {
		t.Fatalf("%d modes under test; the contract declares seven", len(modes))
	}
	for _, mode := range modes {
		opts := options()
		opts.Mode = mode
		k, err := core.New(opts, fixedClock(base()), ids())
		switch {
		case mode == controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE:
			if err != nil || k == nil {
				t.Errorf("ENFORCE: New = (%v, %v), want a kernel", k, err)
			}
			continue
		case !errors.Is(err, core.ErrMode):
			t.Errorf("mode %d: New = %v, want ErrMode", mode, err)
		case k != nil:
			t.Errorf("mode %d: New returned a kernel beside the refusal", mode)
		}
		declared := values.ByNumber(mode.Number()) != nil && mode != controlv1.EnforcementMode_ENFORCEMENT_MODE_UNSPECIFIED
		if planned := strings.Contains(err.Error(), "planned"); planned != declared {
			t.Errorf("mode %d: refusal %q; a declared mode is planned, the zero value and an undeclared number are not", mode, err)
		}
	}
}

func TestNewRefusesANilClockOrIDSource(t *testing.T) {
	if k, err := core.New(options(), nil, ids()); !errors.Is(err, core.ErrNoClock) || k != nil {
		t.Errorf("nil clock: New = (%v, %v), want ErrNoClock and no kernel", k, err)
	}
	if k, err := core.New(options(), fixedClock(base()), nil); !errors.Is(err, core.ErrNoIDSource) || k != nil {
		t.Errorf("nil id source: New = (%v, %v), want ErrNoIDSource and no kernel", k, err)
	}
}

// TestNewHoldsApplicableToTheCatalogue: every name of the catalogue is
// accepted, and a spelling one character off, the empty string, and a
// catalogue name in another case are refused. Two accepted and one refused
// name in one list is a refusal.
func TestNewHoldsApplicableToTheCatalogue(t *testing.T) {
	catalogue := []string{
		"redact_fields", "read_only", "restrict_resources", "require_idempotency_key",
		"cap_amount", "cap_rate", "require_sandbox", "second_approver", "emit_alert",
		"shorten_timeout", "deny_external_sink",
	}
	opts := options()
	opts.Applicable = catalogue
	if _, err := core.New(opts, fixedClock(base()), ids()); err != nil {
		t.Fatalf("the whole catalogue: New = %v, want a kernel", err)
	}
	opts.Applicable = nil
	if _, err := core.New(opts, fixedClock(base()), ids()); err != nil {
		t.Fatalf("no applicable type: New = %v, want a kernel", err)
	}
	for _, bad := range []string{"cap_amounts", "", "CAP_AMOUNT", " cap_amount", "cap_amount "} {
		opts.Applicable = []string{"redact_fields", bad, "cap_rate"}
		k, err := core.New(opts, fixedClock(base()), ids())
		if !errors.Is(err, core.ErrApplicable) || k != nil {
			t.Errorf("Applicable %q: New = (%v, %v), want ErrApplicable and no kernel", bad, k, err)
		}
	}
}

// TestNewRefusesAMaxStaleThatIsNotPositive: zero and a negative budget are
// refused; one nanosecond is the smallest budget accepted.
func TestNewRefusesAMaxStaleThatIsNotPositive(t *testing.T) {
	for _, d := range []time.Duration{0, -1, -time.Hour} {
		opts := options()
		opts.MaxStale = d
		if k, err := core.New(opts, fixedClock(base()), ids()); !errors.Is(err, core.ErrMaxStale) || k != nil {
			t.Errorf("MaxStale %v: New = (%v, %v), want ErrMaxStale and no kernel", d, k, err)
		}
	}
	opts := options()
	opts.MaxStale = time.Nanosecond
	if _, err := core.New(opts, fixedClock(base()), ids()); err != nil {
		t.Errorf("MaxStale 1ns: New = %v, want a kernel", err)
	}
}

// TestNewDoesNotAliasApplicable: a caller that changes its slice after New
// changes nothing the kernel reads.
func TestNewDoesNotAliasApplicable(t *testing.T) {
	applicable := []string{"redact_fields"}
	opts := options()
	opts.Applicable = applicable
	k := kernel(t, opts, fixedClock(base()))
	applicable[0] = "require_sandbox"
	// sandboxedReads carries require_sandbox, non-advisory: applicable only if
	// the kernel read the slice after the write.
	out := decide(k, request(readEnvelope()), snapshot(t, sandboxedReads))
	check(t, out, expect{verdict: verdictDeny, action: core.Block, codes: []string{codeObligationsAttached, codeObligationNotUnderstood}})
}

// TestNilKernelFailsClosed: a kernel nobody built has no clock and no id
// source, and it still decides INDETERMINATE and Block rather than crash. Its
// decision_id and decided_at are empty on purpose: there is nothing to mint
// them from, and a value there would have come from somewhere unexpected.
func TestNilKernelFailsClosed(t *testing.T) {
	for name, k := range map[string]*core.Kernel{"nil": nil, "zero": {}} {
		out := decide(k, request(readEnvelope()), snapshot(t, allowReads))
		if out.Decision == nil {
			t.Fatalf("%s kernel: Decision is nil", name)
		}
		if out.Decision.GetDecisionId() != "" || out.Decision.GetDecidedAt() != nil {
			t.Errorf("%s kernel: decision_id %q, decided_at %v; want both empty, this path has no id source and no clock", name, out.Decision.GetDecisionId(), out.Decision.GetDecidedAt())
		}
		if out.Decision.GetVerdict() != verdictIndeterminate || out.Action != core.Block {
			t.Errorf("%s kernel: (%s, %d), want INDETERMINATE and Block", name, out.Decision.GetVerdict(), out.Action)
		}
		if out.Decision.GetPolicyFreshness() != stale {
			t.Errorf("%s kernel: freshness %s, want STALE", name, out.Decision.GetPolicyFreshness())
		}
		if !slices.Equal(out.Decision.GetReasonCodes(), []string{codePolicyUnavailable}) {
			t.Errorf("%s kernel: codes %q, want POLICY_UNAVAILABLE alone", name, out.Decision.GetReasonCodes())
		}
	}
}
