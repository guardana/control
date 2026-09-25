package match

import controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

// The reason codes this package emits. They are literals and never read from
// the registry: nothing on the decision path consults a code's documented
// verdict (ADR-0012). codes_internal_test.go holds each one to its entry.
const (
	codeRuleAllow           = "RULE_ALLOW"
	codeRuleDeny            = "RULE_DENY"
	codeApprovalRequired    = "APPROVAL_REQUIRED"
	codeObligationsAttached = "OBLIGATIONS_ATTACHED"
	codeEnvironmentBoundary = "ENVIRONMENT_BOUNDARY"
	codeOutOfScopeAction    = "OUT_OF_SCOPE_ACTION"
	codeToxicFlow           = "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL"
	codeRuleUndetermined    = "RULE_UNDETERMINED"
	codeNoMatchingRule      = "NO_MATCHING_RULE"
	codePolicyUnavailable   = "POLICY_UNAVAILABLE"
)

// defaultReason is a rule effect's own code, and "" for a verdict that is not
// a rule effect.
func defaultReason(effect controlv1.Verdict) string {
	switch effect {
	case controlv1.Verdict_VERDICT_ALLOW:
		return codeRuleAllow
	case controlv1.Verdict_VERDICT_DENY:
		return codeRuleDeny
	case controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:
		return codeApprovalRequired
	case controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS:
		return codeObligationsAttached
	}
	return ""
}

// reasonFor is the code a matched rule contributes: its effect's own when the
// document names none or names that one, and with DENY one of three that say
// why. A kernel fact, TENANT_MISMATCH or POLICY_STALE among them, is never a
// rule's reason, so a rule cannot claim a check the kernel did not make.
func reasonFor(effect controlv1.Verdict, authored string) (string, bool) {
	own := defaultReason(effect)
	switch {
	case own == "":
		return "", false
	case authored == "" || authored == own:
		return own, true
	case effect == controlv1.Verdict_VERDICT_DENY:
		switch authored {
		case codeEnvironmentBoundary, codeOutOfScopeAction, codeToxicFlow:
			return authored, true
		}
	}
	return "", false
}
