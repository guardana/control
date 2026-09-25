package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// TestTheApproverLinksNeitherTheGatewayNorEvidence: this binary answers an
// approval by writing a record into a directory. Reading that record back,
// deciding on it and appending evidence are the plane's side of the seam, and
// a build that reached either from here would put both sides in one binary and
// make the compiler's separation of the two handles decoration. `go list
// -deps` walks the non-test build transitively.
func TestTheApproverLinksNeitherTheGatewayNorEvidence(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	// A listing that does not reach the store this binary answers through
	// examined some other package, and would pass whatever it found.
	if !slices.Contains(deps, brand.ModulePath+"/internal/approvals") {
		t.Fatalf("go list -deps reached %d package(s) and not the approvals store; nothing was examined", len(deps))
	}
	for _, tree := range []string{"/internal/gateway", "/internal/evidence"} {
		for _, dep := range deps {
			if dep == brand.ModulePath+tree || strings.HasPrefix(dep, brand.ModulePath+tree+"/") {
				t.Errorf("this binary reaches %s", dep)
			}
		}
	}
}
