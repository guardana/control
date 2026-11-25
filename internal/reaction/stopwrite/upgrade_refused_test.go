package stopwrite_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stopwrite"
)

// v2ListOf writes into dir a 2.0 list under r at serial holding stop, of
// scope run and project shop, and returns the list's head digest.
func v2ListOf(t *testing.T, dir string, r reaction.Route, serial int64, stop reaction.Stop) string {
	t.Helper()
	body, err := reaction.HeaderBody(reaction.Header{ListID: "lst-old", RouteID: r.ID(), RouteSerial: r.Serial(), RouteDigest: r.Digest(),
		Signed: reaction.HeaderSigned{KeyID: operatorID(), TenantID: "acme", ProjectID: "shop", File: listPath(dir), Serial: serial}})
	noErr(t, err)
	header, err := reaction.SignedHeaderLine(envelopeBy(t, operatorKey(), reaction.SignedHeaderPayloadType, body))
	noErr(t, err)
	stop.ProjectID, stop.Scope = "shop", reaction.ScopeRun
	line, err := stop.Marshal()
	noErr(t, err)
	appendRaw(t, dir, header)
	appendRaw(t, dir, line)
	return bundle.Digest(body)
}

// TestARefusedCarryFromA20ListNamesARemedyThatWorks: a 2.0 list's stop of
// run-1 lasting an hour, under a rule that may stop and under a retired
// version, is carried into a route whose one run rule of refund lasts a
// minute. The carry is refused naming the stop and two remedies, not the
// lift under the old route, which refuses a v1alpha2 route; the route rule
// it names, lasting the hour, then carries the stop and a plane stops run-1.
func TestARefusedCarryFromA20ListNamesARemedyThatWorks(t *testing.T) {
	for _, c := range []struct{ rule, keeps string }{
		{"STEP_OUTSIDE_PROCEDURE", `rule STEP_OUTSIDE_PROCEDURE of procedure "refund" version "1" at scope run`},
		{"DEADLINE_EXCEEDED", `a rule of procedure "refund" version "1" at scope run`},
	} {
		t.Run(c.rule, func(t *testing.T) {
			from := emptyDir(t)
			fromRoute := v2RouteWith(t, from, 3, v2Rule(t, c.rule))
			stop := stopOf(t, "f-1", "run-1", clock0.Add(-time.Minute))
			stop.RuleID = c.rule
			head := v2ListOf(t, from, fromRoute, 5, stop)
			before := content(t, from)

			into := emptyDir(t)
			short := retiredWideRoute(t, into, 4, false, refundRule(t, "STEP_OUTSIDE_PROCEDURE", "1", "run", "60"))
			_, err := stopwrite.CarrySigned(bg, from, fromRoute, into, short, clock0, signer(), nil)
			if !errors.Is(err, stopwrite.ErrRefused) || !errors.Is(err, reaction.ErrClaimLifetime) {
				t.Fatalf("CarrySigned into a route whose rule lasts a minute: %v; want %v", err, reaction.ErrClaimLifetime)
			}
			for _, want := range []string{`run "run-1" at line 2`, `finding "f-1" under rule ` + c.rule + " 1",
				"give the new route " + c.keeps + " with expires_seconds of at least 3600",
				"start a new list with stops init under a new stop list floor (stops floor init)"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal names no %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "stops lift --") {
				t.Errorf("the refusal names a lift under a v1alpha2 route: %v", err)
			}
			lift, err := reaction.SignedLift{KeyID: operatorID(), ListHead: head, RunID: "run-1", ThroughLine: 2,
				TenantID: "acme", Project: "shop", File: listPath(from)}.Payload()
			noErr(t, err)
			if _, err := stopwrite.AppendLift(bg, from, fromRoute, reaction.LiftLine{RunID: "run-1", ThroughLine: 2,
				Envelope: envelopeBy(t, operatorKey(), reaction.SignedLiftPayloadType, lift)}, clock0); !errors.Is(err, reaction.ErrRouteUpgrade) {
				t.Errorf("a lift under the v1alpha2 route: %v; want %v", err, reaction.ErrRouteUpgrade)
			}
			if string(content(t, from)) != string(before) {
				t.Fatal("a refused carry or lift changed the old list")
			}

			kept := retiredWideRoute(t, into, 4, false, refundRule(t, "STEP_OUTSIDE_PROCEDURE", "1", "run", "3600"))
			if got, err := stopwrite.CarrySigned(bg, from, fromRoute, into, kept, clock0, signer(), nil); err != nil || got.Stops != 1 {
				t.Fatalf("CarrySigned into the route the refusal names: %+v, %v", got, err)
			}
			if st, _, _ := planeOn(t, into, kept).Current().ForSubject(reaction.Subject{Tenant: "acme", Run: "run-1"}, clock0, time.Time{}); st != reaction.Stopped {
				t.Errorf("run-1 on the carried list: %s, want %s", st, reaction.Stopped)
			}
		})
	}
}
