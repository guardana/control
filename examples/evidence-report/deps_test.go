package main

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// goList runs go list in this package's directory. A missing go fails the
// test: a dependency check that did not run passes nothing.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	//nolint:gosec // G204: the program is "go" and every argument is a literal of this file.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.Fields(stdout.String())
}

// TestImportsOnlyTheWireContract holds the consumer to what a program
// outside this module has: of this module it reaches the generated wire
// contract package and nothing else, no internal/ and no pkg/ package.
//
// The tests of this package import internal/evidence and internal/trailfile
// to hold the reader to the plane's chain and the exporter's bytes. That is
// sound only because go list -deps without -test lists what the program
// imports and not what its tests do; asking with -test would fail this test.
func TestImportsOnlyTheWireContract(t *testing.T) {
	self := goList(t, "-f", "{{.ImportPath}} {{.Module.Path}}", ".")
	if len(self) != 2 {
		t.Fatalf("go list named %q for this package, want its import path and module", self)
	}
	pkg, module := self[0], self[1]
	contract := reflect.TypeFor[controlv1.Event]().PkgPath()
	deps := goList(t, "-deps", "-f", "{{.ImportPath}}", ".")
	reached := false
	for _, dep := range deps {
		switch {
		case dep == contract:
			reached = true
		case dep == pkg:
		case dep == module || strings.HasPrefix(dep, module+"/"):
			t.Errorf("%s depends on %s; of this module it may reach only %s", pkg, dep, contract)
		}
	}
	if !reached {
		t.Fatalf("go list -deps named %d package(s) and not %s, which this package decodes with: the listing examined nothing", len(deps), contract)
	}
}
