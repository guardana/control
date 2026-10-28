package gateway_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/pkg/contract"
)

// refusalShape is one way an adapter can hand a translation in: whole, with
// nothing classifying it, or refused for something else, alone or beside
// the unclassified cause.
type refusalShape struct {
	name string
	// refused: the translation is refused for more than nothing classifying
	// it, so no mode may hand it out.
	refused bool
	admit   func() gateway.Admission
}

func unclassifiedEnvelope() *controlv1.ActionEnvelope {
	env := readEnvelope()
	env.Action.Effect = controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED
	return env
}

func refusing(env *controlv1.ActionEnvelope, refusal error) func() gateway.Admission {
	return func() gateway.Admission {
		a := admission(env, []byte(`{}`))
		a.Refusal = refusal
		return a
	}
}

func refusalShapes() []refusalShape {
	unclassified := fmt.Errorf("%w: no entry names the tool", gateway.ErrUnclassified)
	forged := readEnvelope()
	forged.Context.Tags = []string{"flow.v1.untrusted=false"}
	missing := readEnvelope()
	missing.Principal.Id = ""
	oversized := readEnvelope()
	oversized.Action.Name = strings.Repeat("n", contract.MaxStringBytes+1)
	undeclared := readEnvelope()
	undeclared.Action.Effect = controlv1.EffectClass(99)
	return []refusalShape{
		{"no refusal reported, a required field missing", true, refusing(missing, nil)},
		{"no refusal reported, a string past its bound", true, refusing(oversized, nil)},
		{"no refusal reported, an effect no build declares", true, refusing(undeclared, nil)},
		{"complete", false, refusing(readEnvelope(), nil)},
		{"unclassified alone", false, refusing(unclassifiedEnvelope(), unclassified)},
		{"unclassified, wrapped once more", false, refusing(unclassifiedEnvelope(), fmt.Errorf("adapter: %w", unclassified))},
		{"the bare sentinel", false, refusing(unclassifiedEnvelope(), gateway.ErrUnclassified)},
		{"a bound joined with unclassified", true, refusing(unclassifiedEnvelope(), errors.Join(contract.ErrTooLarge, gateway.ErrUnclassified))},
		{"unclassified joined with a bound", true, refusing(unclassifiedEnvelope(), errors.Join(gateway.ErrUnclassified, contract.ErrTooLarge))},
		{"unclassified and an invalid value in one wrap", true, refusing(unclassifiedEnvelope(), fmt.Errorf("%w: %w", gateway.ErrUnclassified, contract.ErrInvalidValue))},
		{"an invalid value and unclassified in one wrap", true, refusing(unclassifiedEnvelope(), fmt.Errorf("%w: %w", contract.ErrInvalidValue, gateway.ErrUnclassified))},
		{"a join nested inside a wrap", true, refusing(unclassifiedEnvelope(), fmt.Errorf("adapter: %w", errors.Join(unclassified, contract.ErrMissingField)))},
		{"a contract refusal", true, refusing(readEnvelope(), &contract.ValidationError{Field: "action.name", Err: contract.ErrMissingField})},
		{"a contract refusal wrapping unclassified", true, refusing(unclassifiedEnvelope(), &contract.ValidationError{Field: "arguments.canonical_hash", Err: fmt.Errorf("%w: %w", contract.ErrInvalidValue, unclassified)})},
		{"a refusal that names no sentinel", true, refusing(readEnvelope(), errors.New("the decoder gave up"))},
		{"no envelope", true, refusing(nil, nil)},
		{"a forged flow tag", true, refusing(forged, nil)},
	}
}

// declaredModes is every enforcement mode New starts a plane in.
func declaredModes(t *testing.T) []controlv1.EnforcementMode {
	t.Helper()
	var out []controlv1.EnforcementMode
	values := controlv1.EnforcementMode(0).Descriptor().Values()
	for i := range values.Len() {
		mode := controlv1.EnforcementMode(values.Get(i).Number())
		cfg := validConfig(t)
		cfg.Mode = mode
		if _, err := gateway.New(cfg); err == nil {
			out = append(out, mode)
		}
	}
	if !slices.Contains(out, modeObserve) || !slices.Contains(out, modeEnforce) {
		t.Fatalf("declared modes %v lack OBSERVE or ENFORCE", out)
	}
	return out
}

// TestARefusedTranslationIsNeverHandedOut: in every mode New starts, Admit
// hands out a translation only when it is whole or nothing but its
// classification is missing, and the second only in OBSERVE. Whatever joins
// the unclassified cause with another is refused, and no refused call is
// recorded as started.
func TestARefusedTranslationIsNeverHandedOut(t *testing.T) {
	for _, mode := range declaredModes(t) {
		for _, shape := range refusalShapes() {
			t.Run(mode.String()+"/"+shape.name, func(t *testing.T) {
				h := build(t, mode, snapshot(t, allowReads))
				expectHandedOut(t, mode, shape, h.admitA(shape.admit()), slices.Contains(h.kinds(), kindStarted))
			})
		}
	}
}

// expectHandedOut holds what Admit did with shape in mode, and whether the
// trail says it started, to what the shape allows.
func expectHandedOut(t *testing.T, mode controlv1.EnforcementMode, shape refusalShape, d gateway.Disposition, started bool) {
	t.Helper()
	runs := d.Action == core.Execute || d.Action == core.ExecuteWithObligations
	if shape.refused {
		if runs || d.Action == core.AwaitApproval || started {
			t.Errorf("Action = %d, started %v for a refused translation, want Block (%v)", d.Action, started, d.Decision.GetReasonCodes())
		}
		return
	}
	switch {
	case shape.name != "complete" && mode != modeObserve && runs:
		t.Errorf("Action = %d for an unclassified call outside OBSERVE, want a block", d.Action)
	case mode == modeObserve && !runs:
		t.Errorf("OBSERVE Action = %d for %s, want it handed out (%v)", d.Action, shape.name, d.Decision.GetReasonCodes())
	case runs != started:
		t.Errorf("handed out %v, recorded as started %v", runs, started)
	}
}

// TestObserveRecordsARefusedTranslationAsEnforceDoes: OBSERVE answers a
// translation refused for more than its classification with the kernel's
// own INDETERMINATE and the refusal's code, on the trail ENFORCE writes.
func TestObserveRecordsARefusedTranslationAsEnforceDoes(t *testing.T) {
	refusal := errors.Join(contract.ErrTooLarge, gateway.ErrUnclassified)
	for _, mode := range []controlv1.EnforcementMode{modeObserve, modeEnforce} {
		t.Run(mode.String(), func(t *testing.T) {
			h := build(t, mode, snapshot(t, allowReads))
			d := h.admitA(refusing(unclassifiedEnvelope(), refusal)())
			expectBlock(t, d, verdictIndeterminate, "LIMIT_EXCEEDED", kernelPDP)
			want := []controlv1.EventKind{kindProposed, kindDecided, kindBlocked}
			if got := h.kinds(); !slices.Equal(got, want) {
				t.Errorf("trail = %v, want %v", got, want)
			}
			if s := h.p.Stats(); s.Executed != 0 || s.Blocks["LIMIT_EXCEEDED"] != 1 {
				t.Errorf("Stats: Executed %d, Blocks %v, want none executed and one LIMIT_EXCEEDED", s.Executed, s.Blocks)
			}
		})
	}
}

// TestARefusedTranslationMintsNoRun: a run is minted for a call that is
// handed out or could be, so OBSERVE mints one for an unclassified call and
// none for a translation refused for anything else, in any mode.
func TestARefusedTranslationMintsNoRun(t *testing.T) {
	joined := errors.Join(contract.ErrTooLarge, gateway.ErrUnclassified)
	for _, c := range []struct {
		name string
		mode controlv1.EnforcementMode
		in   func() gateway.Admission
		runs int
	}{
		{"OBSERVE, unclassified", modeObserve, refusing(unclassifiedEnvelope(), gateway.ErrUnclassified), 1},
		{"OBSERVE, joined", modeObserve, refusing(unclassifiedEnvelope(), joined), 0},
		{"OBSERVE, a contract refusal", modeObserve, refusing(readEnvelope(), contract.ErrMissingField), 0},
		{"ENFORCE, unclassified", modeEnforce, refusing(unclassifiedEnvelope(), gateway.ErrUnclassified), 0},
		{"ENFORCE, complete", modeEnforce, refusing(readEnvelope(), nil), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := build(t, c.mode, snapshot(t, allowReads))
			h.admitA(c.in())
			if got := h.p.Stats().Runs; got != c.runs {
				t.Errorf("Stats().Runs = %d, want %d", got, c.runs)
			}
		})
	}
}
