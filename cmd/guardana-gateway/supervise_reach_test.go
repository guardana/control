package main

import (
	"slices"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// supervisionTrees are the code that judges a run and keeps its findings,
// and the finding records' package; notifyTree hands findings on.
var supervisionTrees = []string{
	brand.ModulePath + "/internal/supervise",
	brand.ModulePath + "/internal/findinglog",
	brand.ModulePath + "/api/gen/go/guardana/control/finding/v1alpha1",
}

const notifyTree = brand.ModulePath + "/internal/notify"

// TestNoSupervisionReachesThePlane: no verdict changes on a finding
// (ADR-0045), so neither the plane's binary, its five guarded trees nor the
// adapters depend, on any release platform, on what judges a run, keeps its
// findings or delivers them.
func TestNoSupervisionReachesThePlane(t *testing.T) {
	t.Parallel()
	trees := append(slices.Clone(supervisionTrees), notifyTree)
	for _, l := range dependencies(t, planePatterns...) {
		if len(l.deps) < 100 {
			t.Errorf("go list for %s named %d packages; the listing examined too little", l.platform, len(l.deps))
			continue
		}
		for _, dep := range reached(l.deps, trees) {
			t.Errorf("%s is reachable from the plane on %s", dep, l.platform)
		}
	}
}

// TestTheSupervisionReachCheckFindsWhatTheControlImports is the negative
// control: the control's command line imports each supervision tree, and the
// same listing and match find it there on every release platform.
func TestTheSupervisionReachCheckFindsWhatTheControlImports(t *testing.T) {
	t.Parallel()
	trees := append(slices.Clone(supervisionTrees), notifyTree)
	for _, l := range dependencies(t, "../"+brand.CLI) {
		for _, tree := range unreached(reached(l.deps, trees), trees) {
			t.Errorf("the control's command line imports %s on %s, and the check did not find it", tree, l.platform)
		}
	}
}
