package refundsupervision

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/guardana/control/internal/observe"
	"github.com/guardana/control/internal/reaction"
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

// liftPlaceholder is the line of route.json the lift key's public line takes.
const liftPlaceholder = "LIFT_PUBLIC_KEY_FROM_KEYGEN"

// TestTheRouteAllowsOnlyTheConfirmedRule: route.json, its lift key filled
// in, is read by the reader route sign uses, names the plane's tenant, and
// allows one rule, the one the example's confirmed finding raises, under the
// digest supervise gives the procedure; a route under any other digest stops
// nothing.
func TestTheRouteAllowsOnlyTheConfirmedRule(t *testing.T) {
	t.Parallel()
	doc := readFile(t, "route.json")
	if n := strings.Count(doc, liftPlaceholder); n != 1 {
		t.Fatalf("route.json holds %s %d times, want once", liftPlaceholder, n)
	}
	lift := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x4c}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	r, err := reaction.ParseRoute([]byte(strings.Replace(doc, liftPlaceholder, base64.StdEncoding.EncodeToString(lift), 1)))
	if err != nil {
		t.Fatalf("the route is refused: %v", err)
	}
	p, err := supervise.ReadProcedure([]byte(readFile(t, "refund.procedure.json")))
	if err != nil {
		t.Fatalf("the procedure is refused: %v", err)
	}
	want := reaction.Rule{ProcedureID: p.ID(), ProcedureVersion: p.Version(), ProcedureDigest: p.Digest(),
		RuleID: "REPEATED_DENIAL", RuleVersion: supervise.RuleVersion}
	if rules := r.Rules(); len(rules) != 1 || rules[0] != want {
		t.Errorf("the route's rules are %+v, want only %+v", rules, want)
	}
	if !regexp.MustCompile(`(?m)^tenant_id: ` + regexp.QuoteMeta(r.TenantID()) + "\n").MatchString(readFile(t, "plane.yaml")) {
		t.Errorf("plane.yaml has no top-level tenant_id %q, which the route names", r.TenantID())
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
