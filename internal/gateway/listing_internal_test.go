package gateway

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestTheListingOfEachDecision: only the kernel's own DENY for no matching
// rule, beside an ALLOW rule whose match was unknown, leaves the tool to be
// decided per call. A decision the plane minted is not the kernel's, even
// one that spells the same code, and a pause, a stop or a halt is denied to
// the listing whatever the kernel said.
func TestTheListingOfEachDecision(t *testing.T) {
	of := func(verdict controlv1.Verdict, codes ...string) *controlv1.Decision {
		return &controlv1.Decision{Verdict: verdict, ReasonCodes: codes}
	}
	deny := controlv1.Verdict_VERDICT_DENY
	noMatch := of(deny, "NO_MATCHING_RULE")
	ruleDeny := of(deny, "RULE_DENY")
	noMatchAndMore := of(deny, "NO_MATCHING_RULE", "TENANT_MISMATCH")
	allowed := of(controlv1.Verdict_VERDICT_ALLOW, "RULE_ALLOW")
	obliged := of(controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS, "OBLIGATIONS_ATTACHED")
	approval := of(controlv1.Verdict_VERDICT_REQUIRE_APPROVAL, "APPROVAL_REQUIRED")
	undetermined := of(controlv1.Verdict_VERDICT_INDETERMINATE, "RULE_UNDETERMINED")
	unknown := []string{"allow-order"}
	for _, c := range []struct {
		name             string
		decision, kernel *controlv1.Decision
		unknownAllows    []string
		want             Listing
	}{
		{"the kernel's no match beside an unknown allow", noMatch, noMatch, unknown, ListingPerCall},
		{"the kernel's no match with no unknown allow", noMatch, noMatch, nil, ListingDenied},
		{"a matched DENY beside an unknown allow", ruleDeny, ruleDeny, unknown, ListingDenied},
		{"no match among other codes beside an unknown allow", noMatchAndMore, noMatchAndMore, unknown, ListingDenied},
		{"a plane decision that spells no match", of(deny, "NO_MATCHING_RULE"), noMatch, unknown, ListingDenied},
		{"a pause over the kernel's no match", of(deny, codePaused), noMatch, unknown, ListingDenied},
		{"a stop over the kernel's no match", of(deny, codeRunStopped), noMatch, unknown, ListingDenied},
		{"LOCKDOWN over the kernel's no match", of(deny, codeLockdown), noMatch, unknown, ListingDenied},
		{"a halt over the kernel's no match", of(deny, codeExecutedArgsMismatch), noMatch, unknown, ListingDenied},
		{"ALLOW", allowed, allowed, unknown, ListingDecided},
		{"ALLOW_WITH_OBLIGATIONS", obliged, obliged, nil, ListingDecided},
		{"REQUIRE_APPROVAL", approval, approval, nil, ListingDecided},
		{"INDETERMINATE", undetermined, undetermined, nil, ListingPerCall},
		{"no decision", nil, nil, nil, ListingPerCall},
	} {
		if got := listingOf(c.decision, c.kernel, c.unknownAllows); got != c.want {
			t.Errorf("%s: listing %d, want %d", c.name, got, c.want)
		}
	}
}
