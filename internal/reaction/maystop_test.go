package reaction_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/reaction"
)

// mayStop is every rule a confirmed finding of which may stop a run, spelled
// here rather than read from the package under test.
var mayStop = []string{
	"REPEATED_DENIAL", "STEP_OUTSIDE_PROCEDURE", "DEADLINE_EXCEEDED",
	"RESOURCE_OUTSIDE_RUN", "DENIED_ACTION_RETRIED_ARGUMENTS", "DENIED_ACTION_RETRIED_RESOURCE",
}

// mayNotStop is every other rule supervise has, and ids no rule has, a
// spelling of a stopping rule apart from its own among them.
var mayNotStop = []string{
	"REQUIRED_STEP_SKIPPED", "STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE",
	"DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN",
	"", "REPEATED_DENIALS", "repeated_denial", "REPEATED_DENIAL ", "DENIED_ACTION_RETRIED",
}

func TestMayStopIsTheSixRulesThatMayStop(t *testing.T) {
	for _, id := range mayStop {
		if !reaction.MayStop(id) {
			t.Errorf("MayStop(%q) = false", id)
		}
	}
	for _, id := range mayNotStop {
		if reaction.MayStop(id) {
			t.Errorf("MayStop(%q) = true", id)
		}
	}
	got := reaction.StoppingRules()
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(mayStop))) {
		t.Fatalf("StoppingRules() = %q, want %q", got, mayStop)
	}
	got[0] = "EXCEPTION_TAKEN"
	if reaction.MayStop("EXCEPTION_TAKEN") || !reaction.MayStop(mayStop[0]) {
		t.Fatal("a write into StoppingRules' result reached the table")
	}
}

// routeNaming is a route whose rules name ids in order, all of one procedure.
func routeNaming(t *testing.T, ids ...string) reaction.Route {
	t.Helper()
	rules := make([]string, len(ids))
	for i, id := range ids {
		rules[i] = fmt.Sprintf(`{"procedure_id":"refund","version":"3","digest":"%s","rule_id":%q,"rule_version":"1"}`, procDigest, id)
	}
	doc := withLift(`{"kind":"reaction-route/v1alpha1","route_id":"refunds","serial":7,"tenant_id":"acme",` +
		`"scope":"run","lift_public_key":"LIFTKEY","rules":[` + strings.Join(rules, ",") + `]}`)
	r, err := reaction.ParseRoute([]byte(doc))
	if err != nil {
		t.Fatalf("the case's route does not parse: %v", err)
	}
	return r
}

// TestCheckStoppingRefusesTheWholeRouteForOneRule: a route whose rules are
// the six that may stop passes; the same route with any other rule in any
// place is refused, naming that place.
func TestCheckStoppingRefusesTheWholeRouteForOneRule(t *testing.T) {
	if err := reaction.CheckStopping(routeNaming(t, mayStop...)); err != nil {
		t.Fatalf("a route of the rules that may stop: %v", err)
	}
	// The ids of mayNotStop a route document can hold; ParseRoute refuses the
	// others before this check.
	for _, id := range []string{"REQUIRED_STEP_SKIPPED", "STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE",
		"DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN", "REPEATED_DENIALS", "repeated_denial", "DENIED_ACTION_RETRIED"} {
		for _, place := range []int{0, 3, len(mayStop)} {
			err := reaction.CheckStopping(routeNaming(t, slices.Insert(slices.Clone(mayStop), place, id)...))
			expectOnly(t, fmt.Sprintf("%s at rules[%d]", id, place), err, reaction.ErrRouteRuleStops,
				append(routeRefusals(), reaction.ErrRouteRuleStops))
			if err != nil && !strings.HasPrefix(err.Error(), fmt.Sprintf("rules[%d]: ", place)) {
				t.Errorf("%s at rules[%d]: the refusal names another place: %v", id, place, err)
			}
		}
	}
	if err := reaction.CheckStopping(reaction.Route{}); err != nil {
		t.Errorf("the zero route, which permits nothing: %v", err)
	}
}
