package reaction_test

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// TestReactionReachesNoFindingCode: a plane links this package, and a plane
// links none of supervision, the findings log, the notifier or the finding
// contract's package. Each platform's files are listed: an import only a file
// for another platform carries would escape a listing of this one.
func TestReactionReachesNoFindingCode(t *testing.T) {
	refused := []string{"/internal/supervise", "/internal/findinglog", "/internal/notify", "/api/gen/go/guardana/control/finding"}
	for _, goos := range []string{runtime.GOOS, "windows"} {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("GOOS=%s go list -deps: %v", goos, err)
		}
		deps := strings.Fields(string(out))
		// A listing that does not reach the signer this package verifies
		// with examined some other package, and would pass whatever it found.
		if !slices.Contains(deps, brand.ModulePath+"/internal/policy/bundle") {
			t.Fatalf("GOOS=%s: go list -deps reached %d package(s) and not internal/policy/bundle; nothing was examined", goos, len(deps))
		}
		for _, dep := range deps {
			for _, tree := range refused {
				if dep == brand.ModulePath+tree || strings.HasPrefix(dep, brand.ModulePath+tree+"/") {
					t.Errorf("GOOS=%s: this package reaches %s", goos, dep)
				}
			}
		}
	}
}
