package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/guardana/control/internal/gateway"
)

func TestASilentDecisionPointNobodyAsksNamesTheFlag(t *testing.T) {
	unused := fmt.Errorf("building the pipeline: %w", gateway.ErrDecisionPointUnused)
	if got := silentAdvice(unused, true); !strings.Contains(got.Error(), "drop the flag") || !errors.Is(got, gateway.ErrDecisionPointUnused) {
		t.Errorf("silentAdvice(unused, silent) = %v, want the flag named and the refusal kept", got)
	}
	if got := silentAdvice(unused, false); got.Error() != unused.Error() {
		t.Errorf("silentAdvice(unused, not silent) = %v, want the refusal unchanged", got)
	}
	other := errors.New("another refusal")
	if got := silentAdvice(other, true); got.Error() != other.Error() {
		t.Errorf("silentAdvice(other, silent) = %v, want it unchanged", got)
	}
}
