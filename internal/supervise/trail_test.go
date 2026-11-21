package supervise_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

// cutAfter is x with the events of req after its first n left out, as an
// export holds a trail whose later events are still in transit.
func cutAfter(x supervise.Export, req string, n int) supervise.Export {
	var out []*controlv1.Event
	seen := 0
	for _, ev := range x.Events {
		if ev.GetRequestId() == req {
			seen++
			if seen > n {
				continue
			}
		}
		out = append(out, ev)
	}
	x.Events = out
	return x
}

// TestADenialWhoseBlockHasNotArrivedIsNoPass: on a closed run, a request
// the policy denied whose block is still in transit is a denial and a
// failure no rule has read, so the rules that count them are not checked;
// the same block read makes the count.
func TestADenialWhoseBlockHasNotArrivedIsNoPass(t *testing.T) {
	p := procWith(t)
	closed := supervise.Run{Closed: true}
	whole := evaluate(t, supervise.Input{Procedure: p, Run: closed, Exports: []supervise.Export{export(denials(4)...)}})
	if got := len(only(whole, "REPEATED_DENIAL")); got != 1 {
		t.Fatalf("four blocks read: %d REPEATED_DENIAL findings, want 1", got)
	}
	cut := evaluate(t, supervise.Input{Procedure: p, Run: closed,
		Exports: []supervise.Export{cutAfter(export(denials(4)...), "d4", 2)}})
	for _, rule := range []string{"REPEATED_DENIAL", "CONTINUED_AFTER_FAILURE"} {
		if got := rules(cut)[rule]; !strings.HasPrefix(got, "RULE_STATE_NOT_CHECKED:") || !strings.Contains(got, "d4") {
			t.Errorf("a denial with no block: %s is %q, want not checked naming d4", rule, got)
		}
	}
	retry := supervise02(t, family(), cutAfter(acts(denial(), again()), "d1", 2))
	for _, rule := range []string{ruleArgs, ruleRes} {
		if got := rules(retry)[rule]; !strings.HasPrefix(got, "RULE_STATE_NOT_CHECKED:") {
			t.Errorf("a denial with no block: %s is %q, want not checked", rule, got)
		}
	}
}

// TestARetryDecidedForApprovalIsHeldBeforeItsRequest: the plane writes a
// decision that requires approval before it asks for one, so a trail that
// ends between the two may yet be approved, as one that ends after the ask.
func TestARetryDecidedForApprovalIsHeldBeforeItsRequest(t *testing.T) {
	held := again().ending("held")
	for name, x := range map[string]supervise.Export{
		"the approval asked":         acts(denial(), held),
		"the approval not yet asked": cutAfter(acts(denial(), held), "r1", 2),
	} {
		t.Run(name, func(t *testing.T) {
			res := retrySupervise(t, source(), x)
			if got, want := verdicts(res, ruleArgs), []string{runID + " FINDING_VERDICT_INDETERMINATE"}; !slices.Equal(got, want) {
				t.Fatalf("%s findings %q, want %q:\n%s", ruleArgs, got, want, dump(res.Findings))
			}
		})
	}
}

// withPayloadVersion sets the version of every decision or approval of req
// in x.
func withPayloadVersion(x supervise.Export, req, version string) supervise.Export {
	for _, ev := range x.Events {
		if ev.GetRequestId() != req {
			continue
		}
		if d := ev.GetDecision(); d != nil {
			d.SchemaVersion = version
		}
		if a := ev.GetApproval(); a != nil {
			a.SchemaVersion = version
		}
	}
	return x
}

// TestAPayloadOfAnotherMajorIsNotRead: a decision or an approval carries a
// version of its own, and one of a major this build does not read is not
// read, whatever its event's version: its request is in doubt, so no rule
// reads as checked and nothing confirms from it. The same trail at 1.0
// confirms.
func TestAPayloadOfAnotherMajorIsNotRead(t *testing.T) {
	for _, version := range []string{"1.0", "1.7"} {
		res := retrySupervise(t, source(), withPayloadVersion(acts(denial(), again()), "d1", version))
		if got := rules(res)[ruleArgs]; got != checked {
			t.Fatalf("a decision of %s: %s is %q", version, ruleArgs, got)
		}
	}
	for _, version := range []string{"2.0", "", "1"} {
		res := retrySupervise(t, source(), withPayloadVersion(acts(denial(), again()), "d1", version))
		for _, f := range only(res, ruleArgs) {
			if v := f.GetFinding().GetVerdict(); v != indetermin {
				t.Fatalf("a decision of %q: %s is %s", version, ruleArgs, v)
			}
		}
		if got := rules(res)[ruleArgs]; !strings.Contains(got, `request "d1" is in doubt`) {
			t.Fatalf("a decision of %q: %s is %q", version, ruleArgs, got)
		}
	}
	taken := supervise02(t, family(), acts(lookupCall(), manualRefund(approved)))
	if len(only(taken, ruleTaken)) != 1 || len(only(taken, ruleOutside)) != 0 {
		t.Fatalf("an approval of 1.0 takes no exception:\n%s", dump(taken.Findings))
	}
	unread := supervise02(t, family(), withPayloadVersion(acts(lookupCall(), manualRefund(approved)), "m1", "2.0"))
	if got := only(unread, ruleTaken); len(got) != 0 {
		t.Fatalf("an approval of 2.0 takes an exception:\n%s", dump(got))
	}
	for _, f := range only(unread, ruleOutside) {
		if v := f.GetFinding().GetVerdict(); v != indetermin {
			t.Fatalf("an approval of 2.0: %s is %s", ruleOutside, v)
		}
	}
}

// TestAProposalOfAnotherProjectIsRefused: an event of the run whose proposal
// names another tenant or project is no account of the run's call, and is
// refused as an event of another project is.
func TestAProposalOfAnotherProjectIsRefused(t *testing.T) {
	foreign := func(project, tenant string) supervise.Export {
		x := export(call{req: "w1", tool: "wipe_disk", upstream: "ops"})
		env := x.Events[0].GetProposed()
		env.ProjectId, env.TenantId = project, tenant
		return x
	}
	if _, err := supervise.Evaluate(supervise.Input{Procedure: procWith(t), Run: supervise.Run{ID: runID, Tenant: tenant},
		Exports: []supervise.Export{foreign(proj, tenant)}}); err != nil {
		t.Fatalf("a proposal of the run's own project: %v", err)
	}
	for name, x := range map[string]supervise.Export{
		"another project": foreign("p2", tenant),
		"no project":      foreign("", tenant),
		"another tenant":  foreign(proj, "t2"),
	} {
		t.Run(name, func(t *testing.T) {
			res, err := supervise.Evaluate(supervise.Input{Procedure: procWith(t), Run: supervise.Run{ID: runID, Tenant: tenant},
				Exports: []supervise.Export{x}})
			if !errors.Is(err, supervise.ErrInput) {
				t.Fatalf("Evaluate = %v, %v; want ErrInput", res, err)
			}
		})
	}
}
