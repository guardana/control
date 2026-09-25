package core_test

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/pkg/contract"
)

// goldEnvelope is a READ of a gold-tier order, which denyGold and approveGold
// match.
func goldEnvelope() *controlv1.ActionEnvelope {
	env := readEnvelope()
	env.Resource.Labels = map[string]string{"tier": "gold"}
	return env
}

// TestEachDeterminateVerdictHasItsAction: the four determinate verdicts map to
// their fixed actions, under fail-open too, and obligations travel on the two
// verdicts that carry them.
func TestEachDeterminateVerdictHasItsAction(t *testing.T) {
	refund := request(refundEnvelope(t))
	refund.AuthorizedArgs = refundArgs()
	cases := []struct {
		name  string
		req   core.Request
		rules []string
		want  expect
		obls  int
	}{
		{"a matched DENY", request(goldEnvelope()), []string{allowReads, denyGold}, expect{verdictDeny, core.Block, []string{codeRuleAllow, codeRuleDeny}}, 0},
		{"nothing matched", request(writeEnvelope()), []string{allowReads}, expect{verdictDeny, core.Block, []string{codeNoMatchingRule}}, 0},
		{"REQUIRE_APPROVAL", refund, []string{approveRefunds}, expect{verdictApproval, core.AwaitApproval, []string{codeApprovalRequired}}, 1},
		{"REQUIRE_APPROVAL over an ALLOW", request(goldEnvelope()), []string{allowReads, approveGold}, expect{verdictApproval, core.AwaitApproval, []string{codeRuleAllow, codeApprovalRequired}}, 0},
		{"ALLOW_WITH_OBLIGATIONS", request(readEnvelope()), []string{cappedReads}, expect{verdictObligations, core.ExecuteWithObligations, []string{codeObligationsAttached}}, 2},
		{"ALLOW_WITH_OBLIGATIONS over an ALLOW", request(readEnvelope()), []string{allowReads, cappedReads}, expect{verdictObligations, core.ExecuteWithObligations, []string{codeRuleAllow, codeObligationsAttached}}, 2},
		{"ALLOW", request(readEnvelope()), []string{allowReads}, expect{verdictAllow, core.Execute, []string{codeRuleAllow}}, 0},
	}
	for _, opts := range []core.Options{options(), failOpen()} {
		k := kernelAt(t, opts)
		for _, c := range cases {
			out := decide(k, c.req, snapshot(t, c.rules...))
			check(t, out, c.want)
			if got := len(out.Decision.GetObligations()); got != c.obls {
				t.Errorf("%s: %d obligations, want %d", c.name, got, c.obls)
			}
		}
	}
}

// failClosedCase is one row of the fail-closed table reached through Decide.
type failClosedCase struct {
	name    string
	env     *controlv1.ActionEnvelope
	refusal error    // handed in as the decoder's
	args    []byte   // the authorized arguments
	rules   []string // nil: no snapshot at all
	stale   bool     // the snapshot confirmed one nanosecond past the budget
	open    expect   // with FailOpenRead on
	shut    expect   // with FailOpenRead off
	snap    func() *policy.Snapshot
}

func failClosedCases(t *testing.T) []failClosedCase {
	t.Helper()
	oneSide := readEnvelope()
	oneSide.Resource.TenantId = ""
	cross := readEnvelope()
	cross.Resource.TenantId = "tenant-2"
	expired := readEnvelope()
	expired.Delegation = chain([]string{"read"}, base())
	oneSideWrite := writeEnvelope()
	oneSideWrite.Principal.TenantId = ""
	tabRead, tabWrite := readEnvelope(), writeEnvelope()
	tabRead.RequestId, tabWrite.RequestId = "req\t1", "req\t1"
	otherHash := &controlv1.Arguments{CanonicalHash: argumentsHash(t, []byte(`{"amount":1}`))}
	wrongHashRead, wrongHashWrite := readEnvelope(), writeEnvelope()
	wrongHashRead.Arguments, wrongHashWrite.Arguments = otherHash, proto.CloneOf(otherHash)
	block := func(v controlv1.Verdict, codes ...string) expect { return expect{v, core.Block, codes} }
	run := func(codes ...string) expect {
		return expect{verdictIndeterminate, core.Execute, append(codes, codeFailOpenRead)}
	}
	return []failClosedCase{
		{name: "no bundle, READ", env: readEnvelope(),
			open: run(codePolicyUnavailable), shut: block(verdictIndeterminate, codePolicyUnavailable)},
		{name: "no bundle, WRITE", env: writeEnvelope(),
			open: block(verdictIndeterminate, codePolicyUnavailable), shut: block(verdictIndeterminate, codePolicyUnavailable)},
		// The refusal and digest rows carry no snapshot on purpose: with one,
		// the determinate-verdict condition blocks whatever class the cause
		// was given, and only here does the class of the cause decide.
		{name: "no bundle, a refusal handed in on a READ", env: readEnvelope(), refusal: contract.ErrInvalidValue,
			open: block(verdictIndeterminate, codeInvalidFieldValue), shut: block(verdictIndeterminate, codeInvalidFieldValue)},
		{name: "no bundle, a refusal handed in on a WRITE", env: writeEnvelope(), refusal: contract.ErrInvalidValue,
			open: block(verdictIndeterminate, codeInvalidFieldValue), shut: block(verdictIndeterminate, codeInvalidFieldValue)},
		{name: "no bundle, a READ Validate refuses", env: tabRead,
			open: block(verdictIndeterminate, codeInvalidFieldValue), shut: block(verdictIndeterminate, codeInvalidFieldValue)},
		{name: "no bundle, a WRITE Validate refuses", env: tabWrite,
			open: block(verdictIndeterminate, codeInvalidFieldValue), shut: block(verdictIndeterminate, codeInvalidFieldValue)},
		{name: "no bundle, a hash of other arguments on a READ", env: wrongHashRead, args: []byte(`{"amount":2}`),
			open: block(verdictIndeterminate, codeInvalidFieldValue), shut: block(verdictIndeterminate, codeInvalidFieldValue)},
		{name: "no bundle, a hash of other arguments on a WRITE", env: wrongHashWrite, args: []byte(`{"amount":2}`),
			open: block(verdictIndeterminate, codeInvalidFieldValue), shut: block(verdictIndeterminate, codeInvalidFieldValue)},
		{name: "no bundle, one-sided tenant on a READ: the cost ADR-0012 states", env: oneSide,
			open: run(codePolicyUnavailable), shut: block(verdictIndeterminate, codePolicyUnavailable)},
		{name: "no bundle, cross-tenant READ", env: cross,
			open: block(verdictDeny, codeTenantMismatch, codePolicyUnavailable), shut: block(verdictDeny, codeTenantMismatch, codePolicyUnavailable)},
		{name: "no bundle, expired delegation on a READ", env: expired,
			open: block(verdictDeny, codeDelegationExpired, codePolicyUnavailable), shut: block(verdictDeny, codeDelegationExpired, codePolicyUnavailable)},
		{name: "stale, ALLOW matched, READ", env: readEnvelope(), rules: []string{allowReads}, stale: true,
			open: run(codePolicyStale, codeRuleAllow), shut: block(verdictIndeterminate, codePolicyStale, codeRuleAllow)},
		{name: "stale, ALLOW matched, WRITE", env: writeEnvelope(), rules: []string{allowWrites}, stale: true,
			open: block(verdictIndeterminate, codePolicyStale, codeRuleAllow), shut: block(verdictIndeterminate, codePolicyStale, codeRuleAllow)},
		{name: "stale, an undetermined DENY beside a matched ALLOW", env: readEnvelope(), rules: []string{allowReads, denyGold}, stale: true,
			open: block(verdictIndeterminate, codePolicyStale, codeRuleAllow, codeRuleUndetermined), shut: block(verdictIndeterminate, codePolicyStale, codeRuleAllow, codeRuleUndetermined)},
		{name: "stale and nothing matched", env: readEnvelope(), rules: []string{denyArchive}, stale: true,
			open: block(verdictDeny, codePolicyStale, codeNoMatchingRule), shut: block(verdictDeny, codePolicyStale, codeNoMatchingRule)},
		{name: "stale, a matched DENY is DENY", env: goldEnvelope(), rules: []string{allowReads, denyGold}, stale: true,
			open: block(verdictDeny, codePolicyStale, codeRuleAllow, codeRuleDeny), shut: block(verdictDeny, codePolicyStale, codeRuleAllow, codeRuleDeny)},
		{name: "stale, REQUIRE_APPROVAL matched on a READ", env: goldEnvelope(), rules: []string{allowReads, approveGold}, stale: true,
			open: block(verdictIndeterminate, codePolicyStale, codeRuleAllow, codeApprovalRequired), shut: block(verdictIndeterminate, codePolicyStale, codeRuleAllow, codeApprovalRequired)},
		{name: "stale, ALLOW_WITH_OBLIGATIONS matched on a READ", env: readEnvelope(), rules: []string{cappedReads}, stale: true,
			open: block(verdictIndeterminate, codePolicyStale, codeObligationsAttached), shut: block(verdictIndeterminate, codePolicyStale, codeObligationsAttached)},
		{name: "fresh, an undetermined DENY on a READ", env: readEnvelope(), rules: []string{allowReads, denyGold},
			open: block(verdictIndeterminate, codeRuleAllow, codeRuleUndetermined), shut: block(verdictIndeterminate, codeRuleAllow, codeRuleUndetermined)},
		{name: "fresh, one-sided tenant on a WRITE", env: oneSideWrite, rules: []string{allowWrites},
			open: block(verdictIndeterminate, codeTenantUndetermined, codeRuleAllow), shut: block(verdictIndeterminate, codeTenantUndetermined, codeRuleAllow)},
		{name: "stale, an obligation not understood on a READ", env: readEnvelope(), rules: []string{sandboxedReads}, stale: true,
			open: block(verdictDeny, codePolicyStale, codeObligationsAttached, codeObligationNotUnderstood), shut: block(verdictDeny, codePolicyStale, codeObligationsAttached, codeObligationNotUnderstood)},
		{name: "a snapshot nobody loaded, READ", env: readEnvelope(), snap: func() *policy.Snapshot { return &policy.Snapshot{} },
			open: block(verdictIndeterminate, codePolicyStale, codePolicyUnavailable), shut: block(verdictIndeterminate, codePolicyStale, codePolicyUnavailable)},
	}
}

// TestFailClosedThroughDecide crosses each cause class of the fail-closed
// table (ADR-0012) with a READ and a material class, FailOpenRead on and off.
// FAIL_OPEN_READ_CONFIGURED is present exactly when a read runs, the verdict
// is INDETERMINATE whenever it does, and the digest of the snapshot is on
// every decision made with one.
func TestFailClosedThroughDecide(t *testing.T) {
	cases := failClosedCases(t)
	if len(cases) < 22 {
		t.Fatalf("%d cases; the table has more rows than that", len(cases))
	}
	var ran int
	for _, c := range cases {
		for _, opts := range []core.Options{failOpen(), options()} {
			want := c.shut
			if opts.FailOpenRead {
				want = c.open
			}
			snap := snapshotFor(t, c)
			out := decide(kernelAt(t, opts), requestFor(t, c), snap)
			check(t, out, want)
			checkFailOpenMarker(t, c.name, out)
			if snap != nil && out.Decision.GetPolicyBundleDigest() != snap.Ref().GetDigest() {
				t.Errorf("%s: policy_bundle_digest %q, want the snapshot's %q", c.name, out.Decision.GetPolicyBundleDigest(), snap.Ref().GetDigest())
			}
			if out.Action == core.Execute {
				ran++
			}
		}
	}
	if ran != 3 {
		t.Errorf("%d reads ran under fail-open, want 3: the cross examined the wrong rows", ran)
	}
}

func snapshotFor(t *testing.T, c failClosedCase) *policy.Snapshot {
	t.Helper()
	switch {
	case c.snap != nil:
		return c.snap()
	case c.rules == nil:
		return nil
	case c.stale:
		return snapshotAt(t, document(300, c.rules...), at(-300*time.Second-time.Nanosecond))
	}
	return snapshot(t, c.rules...)
}

func requestFor(t *testing.T, c failClosedCase) core.Request {
	t.Helper()
	req := request(c.env)
	req.Refusal = c.refusal
	req.AuthorizedArgs = c.args
	if c.env.GetAction().GetEffect() == effectTransact {
		req.AuthorizedArgs = refundArgs()
	}
	return req
}

// checkFailOpenMarker: FAIL_OPEN_READ_CONFIGURED is the last code exactly when
// the action is Execute under an INDETERMINATE verdict, and an INDETERMINATE
// verdict is never rewritten.
func checkFailOpenMarker(t *testing.T, name string, out core.Outcome) {
	t.Helper()
	codes := out.Decision.GetReasonCodes()
	marked := slices.Contains(codes, codeFailOpenRead)
	ran := out.Action == core.Execute && out.Decision.GetVerdict() == verdictIndeterminate
	if marked != ran {
		t.Errorf("%s: FAIL_OPEN_READ_CONFIGURED present %v, a read ran %v", name, marked, ran)
	}
	if marked && codes[len(codes)-1] != codeFailOpenRead {
		t.Errorf("%s: FAIL_OPEN_READ_CONFIGURED is not the last code in %q", name, codes)
	}
	if out.Action == core.Execute && out.Decision.GetVerdict() != verdictIndeterminate && out.Decision.GetVerdict() != verdictAllow {
		t.Errorf("%s: Execute under %s", name, out.Decision.GetVerdict())
	}
}

// TestObligationsOutsideApplicable: a non-advisory obligation this kernel
// cannot apply is DENY and Block, on REQUIRE_APPROVAL too; an advisory one
// passes.
func TestObligationsOutsideApplicable(t *testing.T) {
	k := kernelAt(t, failOpen())
	out := decide(k, request(readEnvelope()), snapshot(t, sandboxedReads))
	check(t, out, expect{verdictDeny, core.Block, []string{codeObligationsAttached, codeObligationNotUnderstood}})
	if got := out.Decision.GetObligations(); len(got) != 0 {
		t.Errorf("a DENY carries obligations %v, want none", got)
	}
	out = decide(k, request(readEnvelope()), snapshot(t, advisorySandboxReads))
	check(t, out, expect{verdictObligations, core.ExecuteWithObligations, []string{codeObligationsAttached}})
	if got := out.Decision.GetObligations(); len(got) != 1 || !proto.Equal(got[0], &controlv1.Obligation{Type: "require_sandbox", Advisory: true}) {
		t.Errorf("obligations %v, want the advisory sandbox alone", got)
	}
	check(t, decide(k, request(readEnvelope()), snapshot(t, cappedReads)),
		expect{verdictObligations, core.ExecuteWithObligations, []string{codeObligationsAttached}})
	// REQUIRE_APPROVAL carries obligations too, and cap_amount is outside a
	// kernel that applies nothing.
	none := options()
	none.Applicable = nil
	refund := request(refundEnvelope(t))
	refund.AuthorizedArgs = refundArgs()
	check(t, decide(kernelAt(t, none), refund, snapshot(t, approveRefunds)),
		expect{verdictDeny, core.Block, []string{codeApprovalRequired, codeObligationNotUnderstood}})
	check(t, decide(k, refund, snapshot(t, approveRefunds)),
		expect{verdictApproval, core.AwaitApproval, []string{codeApprovalRequired}})
}

// TestObligationNotUnderstoodIsListedOnce: two rules each carrying an
// obligation the kernel cannot apply give the code once.
func TestObligationNotUnderstoodIsListedOnce(t *testing.T) {
	const twoSandboxes = `{"id":"two-sandboxes","effect":"ALLOW_WITH_OBLIGATIONS","obligations":[{"type":"require_sandbox"},{"type":"read_only"}],"when":{"action":{"effect":["READ"]}}}`
	out := decide(kernelAt(t, options()), request(readEnvelope()), snapshot(t, sandboxedReads, twoSandboxes))
	check(t, out, expect{verdictDeny, core.Block, []string{codeObligationsAttached, codeObligationNotUnderstood}})
}

// TestObligationsTravelOnlyWithTheirVerdicts: an obligation is a condition on
// a call that proceeds, so a decision whose verdict is neither REQUIRE_APPROVAL
// nor ALLOW_WITH_OBLIGATIONS carries none, whatever the matched rules
// attached: a step-8 DENY, and a stale bundle's INDETERMINATE. The two
// verdicts that carry them are pinned in TestEachDeterminateVerdictHasItsAction.
func TestObligationsTravelOnlyWithTheirVerdicts(t *testing.T) {
	refund := request(refundEnvelope(t))
	refund.AuthorizedArgs = refundArgs()
	past := at(-300*time.Second - time.Nanosecond)
	cases := []struct {
		name string
		req  core.Request
		snap *policy.Snapshot
		want expect
	}{
		{"an obligation not understood", request(readEnvelope()), snapshot(t, sandboxedReads),
			expect{verdictDeny, core.Block, []string{codeObligationsAttached, codeObligationNotUnderstood}}},
		{"stale ALLOW_WITH_OBLIGATIONS", request(readEnvelope()), snapshotAt(t, document(300, cappedReads), past),
			expect{verdictIndeterminate, core.Block, []string{codePolicyStale, codeObligationsAttached}}},
		{"stale REQUIRE_APPROVAL", refund, snapshotAt(t, document(300, approveRefunds), past),
			expect{verdictIndeterminate, core.Block, []string{codePolicyStale, codeApprovalRequired}}},
	}
	for _, opts := range []core.Options{options(), failOpen()} {
		k := kernelAt(t, opts)
		for _, c := range cases {
			out := decide(k, c.req, c.snap)
			check(t, out, c.want)
			if got := out.Decision.GetObligations(); len(got) != 0 {
				t.Errorf("%s: obligations %v on a %s, want none", c.name, got, out.Decision.GetVerdict())
			}
		}
	}
}
