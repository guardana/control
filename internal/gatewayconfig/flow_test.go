package gatewayconfig

import (
	"os"
	"strings"
	"testing"
)

const classified = `
overrides:
  - upstream: orders
    tool: read_order
    fingerprint: sha256:00
    effect: READ
    resource_type: order
    trust_zone: PARTNER
`

// TestFlowDefaultsStandWhereTheFileIsSilent: sixty-four runs, and an override
// that says nothing about its results declares none.
func TestFlowDefaultsStandWhereTheFileIsSilent(t *testing.T) {
	cfg := load(t, write(t, classified))
	if cfg.Flow.MaxRuns != 64 {
		t.Errorf("flow.max_runs = %d, want 64", cfg.Flow.MaxRuns)
	}
	o := cfg.Overrides[0]
	trust, errT := o.ReturnsZone()
	read, errS := o.ReturnsLevel()
	if o.ReturnsTrust != "" || o.ReturnsSensitivity != "" || errT != nil || errS != nil ||
		trust.String() != "TRUST_ZONE_UNSPECIFIED" || read.String() != "SENSITIVITY_UNSPECIFIED" {
		t.Errorf("an override silent on its results: %q %q -> %v %v, %v %v", o.ReturnsTrust, o.ReturnsSensitivity, trust, errT, read, errS)
	}
}

// TestAnOverrideDeclaresWhatItsToolReturns: returns.trust and
// returns.sensitivity are read as the contract's values, and leave the
// destination alone.
func TestAnOverrideDeclaresWhatItsToolReturns(t *testing.T) {
	cfg := load(t, write(t, "\nflow:\n  max_runs: 3\n"+classified+"    returns:\n      trust: TRUSTED_INTERNAL\n      sensitivity: CONFIDENTIAL\n"))
	o := cfg.Overrides[0]
	trust, errT := o.ReturnsZone()
	read, errS := o.ReturnsLevel()
	if cfg.Flow.MaxRuns != 3 || errT != nil || errS != nil ||
		trust.String() != "TRUST_ZONE_TRUSTED_INTERNAL" || read.String() != "SENSITIVITY_CONFIDENTIAL" {
		t.Errorf("flow.max_runs %d, returns %v %v, %v %v", cfg.Flow.MaxRuns, trust, errT, read, errS)
	}
	if zone, err := o.Zone(); err != nil || zone.String() != "TRUST_ZONE_PARTNER" {
		t.Errorf("returns.trust changed the destination: %v %v", zone, err)
	}
}

// TestTheBoundOnRunsHasToBePositive: at one a plane keeps one run; at zero or
// below it would keep none and decide every call uncomputed.
func TestTheBoundOnRunsHasToBePositive(t *testing.T) {
	for _, c := range []struct {
		value string
		ok    bool
	}{{"1", true}, {"0", false}, {"-1", false}} {
		path := write(t, "")
		setEnv(t, "flow.max_runs", c.value)
		_, err := Load(path, os.Environ())
		switch {
		case c.ok && err != nil:
			t.Errorf("flow.max_runs=%s was refused: %v", c.value, err)
		case !c.ok && err == nil:
			t.Errorf("flow.max_runs=%s was accepted", c.value)
		case !c.ok && !strings.Contains(err.Error(), "flow.max_runs"):
			t.Errorf("the refusal does not name the key: %v", err)
		}
	}
}

// TestWhatAToolReturnsTakesTheContractsNames: a spelling the contract does not
// declare is refused, naming the key.
func TestWhatAToolReturnsTakesTheContractsNames(t *testing.T) {
	for _, c := range []string{"      trust: INTERNAL\n", "      sensitivity: SENSITIVITY_SECRET\n", "      sensitivity: TOP_SECRET\n"} {
		_, err := Load(write(t, classified+"    returns:\n"+c), os.Environ())
		if err == nil || !strings.Contains(err.Error(), "returns.") {
			t.Errorf("%q: %v", strings.TrimSpace(c), err)
		}
	}
	for _, level := range []string{"PUBLIC", "INTERNAL", "CONFIDENTIAL", "RESTRICTED", "SECRET"} {
		cfg := load(t, write(t, classified+"    returns:\n      sensitivity: "+level+"\n"))
		if got, err := cfg.Overrides[0].ReturnsLevel(); err != nil || got.String() != "SENSITIVITY_"+level {
			t.Errorf("%s: %v %v", level, got, err)
		}
	}
	if _, err := (&OverrideConfig{ReturnsSensitivity: "UNSPECIFIED"}).ReturnsLevel(); err == nil {
		t.Error("ReturnsLevel accepted the zero value's spelling, which says nothing")
	}
}
