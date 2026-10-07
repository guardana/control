package reaction

import (
	"fmt"
	"slices"
)

// stoppingRules is every supervise rule a confirmed finding of which may stop
// a run, and so every rule_id a route may name. It copies supervise's MayStop
// set, since a plane links no supervision code; a test of the route command
// holds the two equal.
var stoppingRules = [...]string{
	"REPEATED_DENIAL",
	"STEP_OUTSIDE_PROCEDURE",
	"DEADLINE_EXCEEDED",
	"RESOURCE_OUTSIDE_RUN",
	"DENIED_ACTION_RETRIED_ARGUMENTS",
	"DENIED_ACTION_RETRIED_RESOURCE",
}

// StoppingRules is a copy of every rule id a route may name.
func StoppingRules() []string { return slices.Clone(stoppingRules[:]) }

// MayStop reports whether a route may name rule id, compared byte for byte.
func MayStop(id string) bool { return slices.Contains(stoppingRules[:], id) }

// CheckStopping refuses a route that names any rule outside StoppingRules.
// ParseRoute and VerifyRoute do not refuse one, so whatever acts on a route
// calls this when it loads it: a route signed by other means than route sign
// is held to the same set.
func CheckStopping(r Route) error {
	for i, rule := range r.rules {
		if !MayStop(rule.RuleID) {
			return fmt.Errorf("rules[%d]: %w", i, ErrRouteRuleStops)
		}
	}
	return nil
}
