package gateway

import (
	"slices"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// Listing is what a preview lets a listing do with a tool. Hiding is the one
// thing a listing subtracts, so the zero value is the one that keeps the
// tool: a listing never hides what nothing decided.
type Listing uint8

const (
	// ListingPerCall is a preview that could not decide without what the
	// call will carry, so the tool stays listed and its call is decided when
	// it is made.
	ListingPerCall Listing = iota
	// ListingDenied is a preview that denies a call to the tool whatever it
	// carries, so a listing may leave the tool out.
	ListingDenied
	// ListingDecided is a preview whose verdict lets a call proceed or wait
	// for approval.
	ListingDecided
)

// codeNoMatchingRule is the kernel's DENY for a call no rule matched. The
// plane reads it off the kernel's decision and never mints it.
const codeNoMatchingRule = "NO_MATCHING_RULE"

// Previewed is a preview's decision and what a listing may do with the tool.
type Previewed struct {
	Decision *controlv1.Decision
	Listing  Listing
}

// listingOf is what a listing may do with a tool previewed as decision, which
// is kernel itself when the plane added no cause of its own. Only the
// kernel's DENY for no matching rule and nothing else, beside an ALLOW rule
// whose match was unknown, is short of a denial: a call carrying what that
// rule reads may be allowed. A plane cause denies whatever the kernel said.
func listingOf(decision, kernel *controlv1.Decision, unknownAllows []string) Listing {
	switch decision.GetVerdict() {
	case verdictDeny:
		if decision == kernel && len(unknownAllows) > 0 && slices.Equal(decision.GetReasonCodes(), []string{codeNoMatchingRule}) {
			return ListingPerCall
		}
		return ListingDenied
	case controlv1.Verdict_VERDICT_ALLOW, controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS, controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:
		return ListingDecided
	}
	return ListingPerCall
}
