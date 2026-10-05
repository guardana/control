package main

import (
	"os/exec"
	"strings"
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
// of their dependencies holds none of them. internal/supervise, the sixth
// guarded tree, reads observations and is not one of the plane's.
func TestNoObservationReachesThePlane(t *testing.T) {
	// This package is the plane's binary; the rest are named from the root.
	deps := dependencies(t, ".", "../../internal/core/...", "../../internal/policy/...", "../../internal/canon/...",
		"../../internal/evidence/...", "../../pkg/contract/...", "../../adapters/...")
	if len(deps) < 100 {
		t.Fatalf("go list named %d packages; the listing examined too little", len(deps))
	}
	for _, dep := range reachable(deps) {
		t.Errorf("%s is reachable from the plane", dep)
	}
}

// TestTheReachCheckFindsWhatTheControlImports is the reach test's negative
// control: the control's command line imports every observation tree, and the
// same listing and match find each of them there.
func TestTheReachCheckFindsWhatTheControlImports(t *testing.T) {
	found := map[string]bool{}
	for _, dep := range reachable(dependencies(t, "../"+brand.CLI)) {
		for _, tree := range observationTrees {
			if within(dep, tree) {
				found[tree] = true
			}
		}
	}
	for _, tree := range observationTrees {
		if !found[tree] {
			t.Errorf("the control's command line imports %s, and the check did not find it", tree)
		}
	}
}

func dependencies(t *testing.T, patterns ...string) []string {
	t.Helper()
	//nolint:gosec // G204: the program and every argument are literals of this file.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, patterns...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	return strings.Fields(string(out))
}

// reachable is every dependency under an observation tree.
func reachable(deps []string) []string {
	var hit []string
	for _, dep := range deps {
		for _, tree := range observationTrees {
			if within(dep, tree) {
				hit = append(hit, dep)
				break
			}
		}
	}
	return hit
}

func within(pkg, tree string) bool {
	return pkg == tree || strings.HasPrefix(pkg, tree+"/")
}
