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
// adapters depend on what judges a run, keeps its findings or delivers them.
func TestNoSupervisionReachesThePlane(t *testing.T) {
	deps := dependencies(t, ".", "../../internal/core/...", "../../internal/policy/...", "../../internal/canon/...",
		"../../internal/evidence/...", "../../pkg/contract/...", "../../adapters/...")
	if len(deps) < 100 {
		t.Fatalf("go list named %d packages; the listing examined too little", len(deps))
	}
	for _, dep := range deps {
		for _, tree := range append(slices.Clone(supervisionTrees), notifyTree) {
			if within(dep, tree) {
				t.Errorf("%s is reachable from the plane", dep)
			}
		}
	}
}

// TestTheSupervisionReachCheckFindsWhatTheControlImports is the negative
// control: the control's command line imports each supervision tree, and the
// same listing and match find it there.
func TestTheSupervisionReachCheckFindsWhatTheControlImports(t *testing.T) {
	want := append(slices.Clone(supervisionTrees), notifyTree)
	found := map[string]bool{}
	for _, dep := range dependencies(t, "../"+brand.CLI) {
		for _, tree := range want {
			if within(dep, tree) {
				found[tree] = true
			}
		}
	}
	for _, tree := range want {
		if !found[tree] {
			t.Errorf("the control's command line imports %s, and the check did not find it", tree)
		}
	}
}
