package main

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// goList runs go list in this package's directory and returns its lines. A
// missing go fails the test: a dependency check that did not run passes
// nothing.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	//nolint:gosec // G204: the program is "go" and every argument is a literal of this file or go list's own output.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.Split(strings.TrimSpace(stdout.String()), "\n")
}

// TestImportsOnlyTheWireContract holds the consumer, its tests included, to
// what a program outside the plane's module has: of that module it reaches
// the generated wire contract package and nothing else, no internal/ and no
// pkg/ package. The module is the contract package's own, so the check does
// not depend on the module this example is built in.
func TestImportsOnlyTheWireContract(t *testing.T) {
	contract := reflect.TypeFor[controlv1.Event]().PkgPath()
	owner := goList(t, "-f", "{{with .Module}}{{.Path}}{{end}}", contract)
	self := goList(t, "-f", "{{with .Module}}{{.Path}}{{end}}", ".")
	if len(owner) != 1 || owner[0] == "" || len(self) != 1 || owner[0] == self[0] {
		t.Fatalf("go list names module %q for %s and %q for this package, want two modules", owner, contract, self)
	}
	module := owner[0]
	pkg := goList(t, "-f", "{{.ImportPath}}", ".")[0]
	// The test build's own line proves the listing covered the tests too.
	testBuild := pkg + " [" + pkg + ".test]"
	deps := goList(t, "-deps", "-test", "-f", "{{.ImportPath}}", ".")
	reached, tested := false, false
	for _, line := range deps {
		tested = tested || line == testBuild
		dep, _, _ := strings.Cut(line, " ")
		switch {
		case dep == contract:
			reached = true
		case dep == module || strings.HasPrefix(dep, module+"/"):
			t.Errorf("this package or its tests depend on %s; of module %s they may reach only %s", dep, module, contract)
		}
	}
	if !tested {
		t.Errorf("go list -deps -test did not list %q: the tests' imports went unexamined", testBuild)
	}
	if !reached {
		t.Fatalf("go list -deps -test named %d package(s) and not %s, which this package decodes with: the listing examined nothing", len(deps), contract)
	}
}
