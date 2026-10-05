package main

import (
	"testing"

	"github.com/guardana/control/internal/brand"
)

// observationTrees are the observation package and the code that imports,
// stores or reads observations.
var observationTrees = []string{
	"github.com/guardana/control/api/gen/go/guardana/control/observe",
	"github.com/guardana/control/internal/ingest",
	"github.com/guardana/control/internal/observe",
	"github.com/guardana/control/internal/observelog",
}

// TestNoObservationReachesThePlane: an observation is testimony and never an
// input to a decision (ADR-0039), so neither the plane's five guarded trees,
// the adapters nor the plane's binary may depend on the observation package or
// on the code that imports, stores or reads observations. A non-test listing
// of their dependencies holds none of them on any release platform.
// internal/supervise, the sixth guarded tree, reads observations and is not
// one of the plane's.
func TestNoObservationReachesThePlane(t *testing.T) {
	t.Parallel()
	for _, l := range dependencies(t, planePatterns...) {
		if len(l.deps) < 100 {
			t.Errorf("go list for %s named %d packages; the listing examined too little", l.platform, len(l.deps))
			continue
		}
		for _, dep := range reached(l.deps, observationTrees) {
			t.Errorf("%s is reachable from the plane on %s", dep, l.platform)
		}
	}
}

// TestTheReachCheckFindsWhatTheControlImports is the reach test's negative
// control: the control's command line imports every observation tree, and the
// same listing and match find each of them there on every release platform.
func TestTheReachCheckFindsWhatTheControlImports(t *testing.T) {
	t.Parallel()
	for _, l := range dependencies(t, "../"+brand.CLI) {
		for _, tree := range unreached(reached(l.deps, observationTrees), observationTrees) {
			t.Errorf("the control's command line imports %s on %s, and the check did not find it", tree, l.platform)
		}
	}
}
