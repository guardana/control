package gateway_test

import (
	"errors"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gateway"
)

// TestEveryDeclaredModeHasARow walks the contract's enum rather than a list
// written here, so a mode added to the contract without a row fails.
func TestEveryDeclaredModeHasARow(t *testing.T) {
	values := controlv1.EnforcementMode(0).Descriptor().Values()
	if values.Len() < 7 {
		t.Fatalf("the contract declares %d modes; the table was written for 7", values.Len())
	}
	for i := range values.Len() {
		mode := controlv1.EnforcementMode(values.Get(i).Number())
		need, err := gateway.Requirements(mode)
		switch {
		case mode == controlv1.EnforcementMode_ENFORCEMENT_MODE_UNSPECIFIED:
			if !errors.Is(err, gateway.ErrMode) {
				t.Errorf("Requirements(UNSPECIFIED) = %+v, %v; want ErrMode", need, err)
			}
		case errors.Is(err, gateway.ErrMode):
			t.Errorf("Requirements(%s) = ErrMode; a declared mode has a row or is planned", mode)
		case err == nil && !need.ObserveRequest:
			t.Errorf("Requirements(%s) needs no ObserveRequest; every mode records", mode)
		}
	}
}

func TestBlockingModesNeedBlock(t *testing.T) {
	cases := []struct {
		mode  controlv1.EnforcementMode
		block bool
		err   error
	}{
		{controlv1.EnforcementMode_ENFORCEMENT_MODE_OBSERVE, false, nil},
		{controlv1.EnforcementMode_ENFORCEMENT_MODE_APPROVE, true, nil},
		{controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, true, nil},
		{controlv1.EnforcementMode_ENFORCEMENT_MODE_LOCKDOWN, true, nil},
		// The planned rows are readable before they are built: SHADOW acts on
		// the enforced decision, so it needs to block; WARN lets every call
		// proceed.
		{controlv1.EnforcementMode_ENFORCEMENT_MODE_SHADOW, true, gateway.ErrModePlanned},
		{controlv1.EnforcementMode_ENFORCEMENT_MODE_WARN, false, gateway.ErrModePlanned},
	}
	for _, tc := range cases {
		need, err := gateway.Requirements(tc.mode)
		if !errors.Is(err, tc.err) || need.Block != tc.block || !need.ObserveRequest || !need.ObserveResult {
			t.Errorf("Requirements(%s) = %+v, %v; want Block=%v with both observes and %v", tc.mode, need, err, tc.block, tc.err)
		}
	}
}

func TestPlannedAndUndeclaredModesAreRefused(t *testing.T) {
	for _, mode := range []controlv1.EnforcementMode{
		controlv1.EnforcementMode_ENFORCEMENT_MODE_SHADOW,
		controlv1.EnforcementMode_ENFORCEMENT_MODE_WARN,
	} {
		if _, err := gateway.Requirements(mode); !errors.Is(err, gateway.ErrModePlanned) {
			t.Errorf("Requirements(%s) = %v; want ErrModePlanned", mode, err)
		}
	}
	if _, err := gateway.Requirements(controlv1.EnforcementMode(99)); !errors.Is(err, gateway.ErrMode) {
		t.Errorf("Requirements(99) = %v; want ErrMode", err)
	}
}
