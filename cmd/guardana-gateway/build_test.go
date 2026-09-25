package main

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gatewayconfig"
)

// TestThePipelineTakesTheConfiguredBoundOnRuns: flow.max_runs is the bound the
// pipeline is built with, for values that are neither the default nor the
// pipeline tests' own.
func TestThePipelineTakesTheConfiguredBoundOnRuns(t *testing.T) {
	for _, runs := range []int{1, 3, 97} {
		p := &plane{cfg: &gatewayconfig.Config{Flow: gatewayconfig.FlowConfig{MaxRuns: runs}}}
		if got := p.pipelineConfig(nil, controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, nil).MaxRuns; got != runs {
			t.Errorf("flow.max_runs %d reaches the pipeline as %d", runs, got)
		}
	}
}
