package reaction

import (
	"fmt"
	"slices"
)

// StoppingRule is a supervise rule at one version.
type StoppingRule struct{ ID, Version string }

// stoppingRules is every supervise rule, at its version, a confirmed finding
// of which may stop a run, and so every rule a route may name. It copies
// supervise's table, since a plane links no supervision code; a test of the
// route command holds the two equal. A rule's next version changes what it
// fires on, so a route naming it is refused until this table names it too.
var stoppingRules = [...]StoppingRule{
	{"REPEATED_DENIAL", "1"},
	{"STEP_OUTSIDE_PROCEDURE", "1"},
	{"DEADLINE_EXCEEDED", "1"},
	{"RESOURCE_OUTSIDE_RUN", "1"},
	{"DENIED_ACTION_RETRIED_ARGUMENTS", "1"},
	{"DENIED_ACTION_RETRIED_RESOURCE", "1"},
}

// StoppingRules is a copy of every rule a route may name.
func StoppingRules() []StoppingRule { return slices.Clone(stoppingRules[:]) }

// MayStop reports whether a route may name rule id at version, both compared
// byte for byte.
func MayStop(id, version string) bool {
	return slices.Contains(stoppingRules[:], StoppingRule{id, version})
}

// CheckStopping refuses a route that names any rule outside StoppingRules.
// VerifyRoute and the stop list's writers call it, so a route read from a
// signed file or handed to a writer is held to the table however it was
// signed. ParseRoute does not, so route sign can first say which of
// supervise's own checks a rule fails.
func CheckStopping(r Route) error {
	for i, rule := range r.rules {
		if !MayStop(rule.RuleID, rule.RuleVersion) {
			return fmt.Errorf("rules[%d]: %w: rule_id %q rule_version %q", i, ErrRouteRuleStops, rule.RuleID, rule.RuleVersion)
		}
	}
	return nil
}
