package refundsupervision

import (
	"os"
	"regexp"
	"testing"

	"github.com/guardana/control/internal/observe"
	"github.com/guardana/control/internal/supervise"
)

// TestTheExamplesFilesAgree: the procedure and the source descriptor are
// read by the readers supervise and observe import use, each step names a
// tool the plane classifies on the upstream it names, and the descriptor's
// tenant and project are the plane's, without which every observation would
// be left out of the run as another tenant's or project's.
func TestTheExamplesFilesAgree(t *testing.T) {
	t.Parallel()
	plane := readFile(t, "plane.yaml")
	raw := readFile(t, "refund.procedure.json")
	p, err := supervise.ReadProcedure([]byte(raw))
	if err != nil {
		t.Fatalf("the procedure is refused: %v", err)
	}
	if len(p.Steps()) != 2 {
		t.Fatalf("the procedure has %d steps, want 2", len(p.Steps()))
	}
	for _, s := range p.Steps() {
		classified := regexp.MustCompile(`(?m)^  - upstream: ` + regexp.QuoteMeta(s.Upstream) +
			`\n    tool: ` + regexp.QuoteMeta(s.Tool) + `\n`)
		if !classified.MatchString(plane) {
			t.Errorf("step %s names %s on %s, which plane.yaml does not classify", s.ID, s.Tool, s.Upstream)
		}
	}
	d, err := observe.ReadDescriptor([]byte(readFile(t, "runtime.source.json")))
	if err != nil {
		t.Fatalf("the source descriptor is refused: %v", err)
	}
	for _, want := range []string{"tenant_id: " + d.GetTenantId() + "\n", "project_id: " + d.GetProjectId() + "\n"} {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want)).MatchString(plane) {
			t.Errorf("plane.yaml has no top-level %q, which the descriptor names", want)
		}
	}
	if d.GetRunAttribute() == "" {
		t.Error("the descriptor names no run attribute, so no observation could claim the run")
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name) //nolint:gosec // G304: this package's own file
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
