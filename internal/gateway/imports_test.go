package gateway_test

import (
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/gateway"
)

// TestPipelineImportsNoProtocol: the pipeline is protocol-neutral (ADR-0013).
// Protocol code lives under adapters and imports this package; a protocol
// library or an adapter reached from here, directly or through another
// package, would make the dependency point both ways. `go list -deps` walks
// the non-test build transitively.
func TestPipelineImportsNoProtocol(t *testing.T) {
	self := reflect.TypeFor[gateway.Pipeline]().PkgPath()
	module := strings.TrimSuffix(self, "/internal/gateway")
	if module == self {
		t.Fatalf("cannot find the module path in %q", self)
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	// A listing that does not reach the kernel examined the wrong package.
	if !slices.Contains(deps, module+"/internal/core") || !slices.Contains(deps, self) {
		t.Fatalf("go list -deps reached %d package(s) and not the kernel or this package; nothing was examined", len(deps))
	}
	for _, dep := range deps {
		switch {
		case strings.HasPrefix(dep, "github.com/modelcontextprotocol/"):
			t.Errorf("the pipeline reaches the MCP library: %s", dep)
		case strings.HasPrefix(dep, module+"/adapters/"), dep == module+"/adapters":
			t.Errorf("the pipeline reaches an adapter: %s", dep)
		}
	}
}
