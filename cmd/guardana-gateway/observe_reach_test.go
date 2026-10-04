package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoObservationReachesThePlane: an observation is testimony and never an
// input to a decision (ADR-0039), so neither the guarded trees, the adapters
// nor the plane's binary may depend on the observation package or on the
// code that imports, stores or reads observations. A non-test listing of
// their dependencies holds none of them.
func TestNoObservationReachesThePlane(t *testing.T) {
	const module = "github.com/guardana/control"
	refused := []string{
		module + "/api/gen/go/guardana/control/observe/",
		module + "/internal/ingest",
		module + "/internal/observe",
		module + "/internal/observelog",
	}
	// This package is the plane's binary; the rest are named from the root.
	patterns := []string{
		".", "../../internal/core/...", "../../internal/policy/...", "../../internal/canon/...",
		"../../internal/evidence/...", "../../pkg/contract/...", "../../adapters/...",
	}
	//nolint:gosec // G204: the program and every argument are literals of this file.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, patterns...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 100 {
		t.Fatalf("go list named %d packages; the listing examined too little", len(deps))
	}
	for _, dep := range deps {
		for _, r := range refused {
			if dep == strings.TrimSuffix(r, "/") || strings.HasPrefix(dep, strings.TrimSuffix(r, "/")+"/") {
				t.Errorf("%s is reachable from the plane", dep)
			}
		}
	}
}
