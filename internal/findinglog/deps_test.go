package findinglog

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// TestTheLogReachesNoObservationOrPlaneCode: a notifier reads this log, so
// neither the observation code nor the plane's evidence and gateway may be
// within its reach. Each platform's files are listed: an import only a file
// for another platform carries would escape a listing of this one.
func TestTheLogReachesNoObservationOrPlaneCode(t *testing.T) {
	for _, goos := range []string{runtime.GOOS, "windows"} {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("GOOS=%s go list -deps: %v", goos, err)
		}
		deps := strings.Fields(string(out))
		// A listing that does not reach the package this log writes through
		// examined some other package, and would pass whatever it found.
		if !slices.Contains(deps, brand.ModulePath+"/internal/files") {
			t.Fatalf("GOOS=%s: go list -deps reached %d package(s) and not internal/files; nothing was examined", goos, len(deps))
		}
		for _, dep := range deps {
			if reached(dep) {
				t.Errorf("GOOS=%s: this package reaches %s", goos, dep)
			}
		}
	}
}

func reached(dep string) bool {
	for _, tree := range []string{"/internal/observe", "/internal/observelog", "/internal/evidence", "/internal/trailfile", "/internal/gateway", "/adapters"} {
		if dep == brand.ModulePath+tree || strings.HasPrefix(dep, brand.ModulePath+tree+"/") {
			return true
		}
	}
	return false
}
