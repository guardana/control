package reaction_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

func TestRetiredRulesAreTheVersionsNoRouteMayName(t *testing.T) {
	if got := reaction.RetiredRules(); !slices.Equal(got, retiredVersions) {
		t.Fatalf("RetiredRules() = %q, want %q", got, retiredVersions)
	}
	for _, r := range retiredVersions {
		if reaction.MayStop(r.ID, r.Version) || reaction.MayStopWider(r.ID, r.Version) || reaction.Superseded(r.ID, r.Version) ||
			!reaction.Retired(r.ID, r.Version) {
			t.Errorf("%s %s: MayStop %v, MayStopWider %v, Superseded %v, Retired %v", r.ID, r.Version, reaction.MayStop(r.ID, r.Version),
				reaction.MayStopWider(r.ID, r.Version), reaction.Superseded(r.ID, r.Version), reaction.Retired(r.ID, r.Version))
		}
	}
	for _, r := range slices.Concat(mayStop, mayNotStop, superseded) {
		if !slices.Contains(retiredVersions, r) && reaction.Retired(r.ID, r.Version) {
			t.Errorf("Retired(%q, %q) = true", r.ID, r.Version)
		}
	}
	got := reaction.RetiredRules()
	got[0].Version = "9"
	if !reaction.Retired("DEADLINE_EXCEEDED", "1") || reaction.Retired("DEADLINE_EXCEEDED", "9") {
		t.Fatal("a write into RetiredRules' result reached the table")
	}
}

// retiredStop is a stop of run-1 for finding under rule id at version, of
// the procedure listRoute names, created at clock0 and lasting life.
func retiredStop(t testing.TB, finding, id, version string, life time.Duration) reaction.Stop {
	s := stopOf(t, finding, "run-1", clock0, life)
	s.RuleID, s.RuleVersion = id, version
	return s
}

// TestAListUnderARouteThatNoLongerNamesARetiredRuleKeepsItsStop: a carry
// brings a stop of a retired version into a list whose route no longer names
// the rule, so the judge keeps it, bounded by MaxLifetime, and it still stops
// its run; Permits, which react asks, refuses it. A stop of a procedure the
// route does not name, of a version never retired, or lasting past
// MaxLifetime is refused.
func TestAListUnderARouteThatNoLongerNamesARetiredRuleKeepsItsStop(t *testing.T) {
	r := listRoute(t)
	for _, rule := range retiredVersions {
		b := newList(t, r)
		b.add(retiredStop(t, "f-1", rule.ID, rule.Version, reaction.MaxLifetime).Marshal())
		l, err := reaction.Judge(r, reaction.Prefix{}, b.bytes(), clock0, poll)
		if err != nil || entryRuns(l.Entries()) != "run-1@2" {
			t.Fatalf("a stop of %s %s: %q, %v", rule.ID, rule.Version, entryRuns(l.Entries()), err)
		}
		s := reaction.Snapshot{}.Next(r, b.bytes(), clock0, poll)
		if got, _, _ := s.ForSubject(reaction.Subject{Tenant: "acme", Run: "run-1"}, clock0.Add(time.Minute), time.Time{}); got != reaction.Stopped {
			t.Errorf("%s %s: the kept stop's run is %s, want %s", rule.ID, rule.Version, got, reaction.Stopped)
		}
		if err := r.Permits(retiredStop(t, "f-1", rule.ID, rule.Version, time.Minute).Claim()); !errors.Is(err, reaction.ErrClaimRule) {
			t.Errorf("Permits of a stop of %s %s: %v, want %v", rule.ID, rule.Version, err, reaction.ErrClaimRule)
		}
	}
	otherProcedure := retiredStop(t, "f-2", "DEADLINE_EXCEEDED", "1", time.Minute)
	otherProcedure.ProcedureVersion = "2"
	for name, stop := range map[string]reaction.Stop{
		"another procedure":       otherProcedure,
		"a version never retired": retiredStop(t, "f-2", "DEADLINE_EXCEEDED", "2", time.Minute),
		"past MaxLifetime":        retiredStop(t, "f-2", "DEADLINE_EXCEEDED", "1", reaction.MaxLifetime+time.Second),
	} {
		b := newList(t, r)
		b.add(stop.Marshal())
		_, err := reaction.Judge(r, reaction.Prefix{}, b.bytes(), clock0, poll)
		expectOnly(t, name, err, reaction.ErrStopRefused, judgeRefusals())
	}
}

// TestAKeptStopOfARetiredRuleKeepsItsScope: under a v1alpha3 route that no
// longer names the retry rules, a principal's stop of
// DENIED_ACTION_RETRIED_ARGUMENTS version "1", which a route could let stop
// one, is kept; the deadline's, which never could, is refused for its scope.
func TestAKeptStopOfARetiredRuleKeepsItsScope(t *testing.T) {
	r := widerRoute(t)
	header, _ := signedHeaderLine(t, r, headerOpts{})
	stop := func(id string) []byte {
		line, err := reaction.Stop{
			EntryID: reaction.EntryID("f-1"), TenantID: sTenant, ProjectID: sProject, Scope: reaction.ScopePrincipal,
			Subject: "alice", RunID: "run-1", FindingID: "f-1", ProcedureID: "refund", ProcedureVersion: "3",
			ProcedureDigest: procDigest, RuleID: id, RuleVersion: "1", CreatedAt: sCreated, ExpiresAt: sCreated.Add(2 * time.Hour),
		}.Marshal()
		if err != nil {
			t.Fatalf("Stop.Marshal: %v", err)
		}
		return line
	}
	l, err := reaction.Judge(r, reaction.Prefix{}, joinLines(header, stop("DENIED_ACTION_RETRIED_ARGUMENTS")), sNow, 0)
	if err != nil || len(l.Entries()) != 1 || l.Entries()[0].Subject != "alice" {
		t.Fatalf("a principal's stop of the retired retry rule: %+v, %v", l.Entries(), err)
	}
	if _, err := reaction.Judge(r, reaction.Prefix{}, joinLines(header, stop("DEADLINE_EXCEEDED")), sNow, 0); !errors.Is(err, reaction.ErrClaimScope) {
		t.Errorf("a principal's stop of the retired deadline: %v, want %v", err, reaction.ErrClaimScope)
	}
}

// TestNoWriterMayAppendAStopOfARetiredVersion: CheckCurrent, which every
// writer asks, refuses a stop of each retired version and passes the same
// stop at a rule that may stop.
func TestNoWriterMayAppendAStopOfARetiredVersion(t *testing.T) {
	for _, rule := range retiredVersions {
		err := reaction.CheckCurrent(retiredStop(t, "f-1", rule.ID, rule.Version, time.Minute))
		if !errors.Is(err, reaction.ErrStopRetired) {
			t.Errorf("CheckCurrent of a stop of %s %s: %v, want %v", rule.ID, rule.Version, err, reaction.ErrStopRetired)
		}
	}
	if err := reaction.CheckCurrent(retiredStop(t, "f-1", "RESOURCE_OUTSIDE_RUN", "1", time.Minute)); err != nil {
		t.Errorf("CheckCurrent of a stop of a rule that may stop: %v", err)
	}
}
