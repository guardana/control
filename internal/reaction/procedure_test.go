package reaction_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/supervise"
)

// TestARouteNamesAProcedureAsAFindingRecordDoes: a route's rule names a
// procedure by the digest supervise gives it, the one a finding record's
// ProcedureRef carries, so a route over a real procedure permits a stop of one
// of its findings.
func TestARouteNamesAProcedureAsAFindingRecordDoes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "refund-supervision", "refund.procedure.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := supervise.ReadProcedure(raw)
	if err != nil {
		t.Fatalf("ReadProcedure: %v", err)
	}
	if d := p.Digest(); len(d) != 64 || strings.Contains(d, ":") {
		t.Fatalf("supervise spells a procedure digest %q; this test expects 64 hex digits and no prefix", d)
	}
	rule := `{"procedure_id":"` + p.ID() + `","version":"` + p.Version() + `","digest":"` + p.Digest() +
		`","rule_id":"STEP_OUTSIDE_PROCEDURE","rule_version":"1"}`
	r, err := reaction.ParseRoute([]byte(routeWith("refunds", "acme", []string{rule})))
	if err != nil {
		t.Fatalf("ParseRoute of a rule naming the procedure's digest: %v", err)
	}
	claim := reaction.StopClaim{
		TenantID: "acme", ProcedureID: p.ID(), ProcedureVersion: p.Version(), ProcedureDigest: p.Digest(),
		RuleID: "STEP_OUTSIDE_PROCEDURE", RuleVersion: "1", CreatedAt: created, ExpiresAt: created.Add(time.Hour),
	}
	if err := r.Permits(claim); err != nil {
		t.Fatalf("Permits of a claim carrying the procedure's digest: %v", err)
	}
	claim.ProcedureDigest = "sha256:" + p.Digest()
	expectOnly(t, "a claim carrying the digest with a prefix", r.Permits(claim), reaction.ErrClaimRule, permitRefusals())
}
