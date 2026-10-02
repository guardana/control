package core_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/pkg/contract"
)

// explainAgrees requires Explain's outcome to be Decide's, field for field,
// from two kernels built alike, so neither the id nor the latency is set aside.
func explainAgrees(t testing.TB, opts core.Options, clock func() time.Time, req core.Request, snap *policy.Snapshot) (core.Outcome, core.Explanation) {
	t.Helper()
	decided := kernel(t, opts, clock).Decide(context.Background(), req, snap)
	explained, e := kernel(t, opts, clock).Explain(context.Background(), req, snap)
	if !proto.Equal(decided.Decision, explained.Decision) || decided.Action != explained.Action || decided.NeedsExternal != explained.NeedsExternal {
		t.Fatalf("Explain decided %v action %d needs %t; Decide %v action %d needs %t",
			explained.Decision, explained.Action, explained.NeedsExternal, decided.Decision, decided.Action, decided.NeedsExternal)
	}
	return explained, e
}

// TestExplainDecidesEveryFixtureAsDecideDoes, and with no bundle too.
func TestExplainDecidesEveryFixtureAsDecideDoes(t *testing.T) {
	for _, f := range readFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			snap := snapshotAt(t, f.document, f.loadedAt)
			out, e := explainAgrees(t, f.opts, fixedClock(f.decidedAt), f.request(), snap)
			check(t, out, f.want)
			if (e.Refusal == nil) != (e.Rules != nil) {
				t.Errorf("refusal %+v beside %d traced rules: a request is refused or evaluated", e.Refusal, len(e.Rules))
			}
			_, none := explainAgrees(t, f.opts, fixedClock(f.decidedAt), f.request(), nil)
			if none.Rules != nil {
				t.Errorf("with no bundle, %d rules traced", len(none.Rules))
			}
		})
	}
}

// TestExplainNamesTheRefusedField: a field the contract requires, a hash the
// arguments do not match, and a message whose unknown member carries text,
// which the explanation never repeats.
func TestExplainNamesTheRefusedField(t *testing.T) {
	unclassified := readEnvelope()
	unclassified.Action.Effect = controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED
	undeclared := readEnvelope()
	undeclared.Action.Effect = 42
	mismatched := refundEnvelope(t)
	mismatched.Arguments.CanonicalHash = argumentsHash(t, []byte(`{"amount": 1}`))
	const sentinel = "sentinel-7f3a"
	leaky, refusal := contract.DecodeJSON([]byte(`{"schemaVersion":"1.0","requestId":"req-1","` + sentinel +
		`":"-----BEGIN PRIVATE KEY-----"}`))
	if refusal == nil {
		t.Fatal("the decoder accepted an unknown member")
	}
	rows := map[string]struct {
		req   core.Request
		field string
	}{
		"no effect class":     {request(unclassified), "action.effect"},
		"an undeclared class": {request(undeclared), "action.effect"},
		"a hash the arguments do not match": {
			core.Request{Envelope: mismatched, AuthorizedArgs: refundArgs(), Flow: request(nil).Flow}, "arguments.canonical_hash",
		},
		"an unknown member": {core.Request{Envelope: leaky, Refusal: refusal, Flow: request(nil).Flow}, ""},
	}
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			_, e := explainAgrees(t, options(), fixedClock(base()), row.req, snapshot(t, allowReads, denyGold))
			if e.Refusal == nil || e.Refusal.Field != row.field {
				t.Fatalf("refusal %+v, want the field %q", e.Refusal, row.field)
			}
			if e.Rules != nil || e.Delegation.State != core.DelegationNotChecked {
				t.Errorf("a refused request was traced on: %+v", e)
			}
			if text := fmt.Sprintf("%+v", e); strings.Contains(text, sentinel) || strings.Contains(text, "PRIVATE KEY") {
				t.Errorf("the explanation repeats the input: %s", text)
			}
		})
	}
}

// TestExplainNamesTheUnstatedTenant: a material call naming one tenant only.
func TestExplainNamesTheUnstatedTenant(t *testing.T) {
	for side, field := range map[string]string{"principal": "principal.tenant_id", "resource": "resource.tenant_id"} {
		env := writeEnvelope()
		if side == "principal" {
			env.Principal.TenantId = ""
		} else {
			env.Resource.TenantId = ""
		}
		out, e := explainAgrees(t, options(), fixedClock(base()), request(env), snapshot(t, allowWrites))
		check(t, out, expect{verdictIndeterminate, core.Block, []string{codeTenantUndetermined, codeRuleAllow}})
		if e.TenantUnstated != field {
			t.Errorf("%s left out: the explanation names %q, want %q", side, e.TenantUnstated, field)
		}
	}
	_, e := explainAgrees(t, options(), fixedClock(base()), request(writeEnvelope()), snapshot(t, allowWrites))
	if e.TenantUnstated != "" {
		t.Errorf("both tenants named, yet the explanation names %q", e.TenantUnstated)
	}
}

// TestExplainSaysWhatBecameOfTheChain: none, refused with its code, passed.
func TestExplainSaysWhatBecameOfTheChain(t *testing.T) {
	expired := readEnvelope()
	expired.Delegation = []*controlv1.Delegation{hop("user-1", "agent-1", []string{"admin"}, base().Add(-time.Minute))}
	passed := readEnvelope()
	passed.Delegation = []*controlv1.Delegation{hop("user-1", "agent-1", []string{"admin"}, base().Add(time.Hour))}
	rows := map[string]struct {
		env  *controlv1.ActionEnvelope
		want core.Delegation
		rule match.Value
	}{
		"no chain":     {readEnvelope(), core.Delegation{State: core.DelegationAbsent}, match.ValueUnknown},
		"a lapsed hop": {expired, core.Delegation{State: core.DelegationRefused, Code: codeDelegationExpired}, match.ValueUnknown},
		"a passed hop": {passed, core.Delegation{State: core.DelegationPassed}, match.ValueYes},
	}
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			_, e := explainAgrees(t, options(), fixedClock(base()), request(row.env), snapshot(t, allowReads, denyAdminScope))
			if e.Delegation != row.want {
				t.Errorf("delegation %+v, want %+v", e.Delegation, row.want)
			}
			i := slices.IndexFunc(e.Rules, func(r match.RuleTrace) bool { return r.ID == "deny-admin-scope" })
			if i < 0 || e.Rules[i].Value != row.rule {
				t.Fatalf("deny-admin-scope traced as %+v, want value %d", e.Rules, row.rule)
			}
		})
	}
}

// TestExplainTracesWhatTheRulesRead: the rules in document order, the one
// that matched, and the one a missing label left undetermined, by name.
func TestExplainTracesWhatTheRulesRead(t *testing.T) {
	out, e := explainAgrees(t, options(), fixedClock(base()), request(readEnvelope()), snapshot(t, denyArchive, allowReads, denyGold))
	check(t, out, expect{verdictIndeterminate, core.Block, []string{codeRuleAllow, codeRuleUndetermined}})
	want := []match.RuleTrace{
		{ID: "deny-archive", Effect: verdictDeny, Value: match.ValueNo, No: []string{"when.action.name"}},
		{ID: "allow-reads", Effect: verdictAllow, Value: match.ValueYes},
		{ID: "deny-gold", Effect: verdictDeny, Value: match.ValueUnknown, Unknown: []match.Unread{
			{Field: `when.resource.labels["tier"]`, Needs: []string{`resource.labels["tier"]`}},
		}},
	}
	if fmt.Sprintf("%+v", e.Rules) != fmt.Sprintf("%+v", want) {
		t.Errorf("traced %+v\nwant   %+v", e.Rules, want)
	}
	if e.Refusal != nil || e.Delegation.State != core.DelegationAbsent {
		t.Errorf("an admitted call without a chain explained as %+v", e)
	}
}

// TestAnUnbuiltKernelExplainsNothing.
func TestAnUnbuiltKernelExplainsNothing(t *testing.T) {
	var k *core.Kernel
	out, e := k.Explain(context.Background(), request(readEnvelope()), snapshot(t, allowReads))
	if out.Action != core.Block || !proto.Equal(out.Decision, k.Decide(context.Background(), request(readEnvelope()), nil).Decision) {
		t.Errorf("an unbuilt kernel explained %v", out)
	}
	if e.Refusal != nil || e.Rules != nil || e.Delegation.State != core.DelegationNotChecked || e.TenantUnstated != "" {
		t.Errorf("an unbuilt kernel explained %+v", e)
	}
}

// TestDecideBuildsNoExplanation: Decide on a three-rule policy allocates less
// than Explain on the same request, so a Decide that built the explanation on
// the request path would show here though its outcome would not change.
func TestDecideBuildsNoExplanation(t *testing.T) {
	k := kernelAt(t, options())
	req, snap := request(readEnvelope()), snapshot(t, denyArchive, allowReads, denyGold)
	decided := testing.AllocsPerRun(50, func() { k.Decide(context.Background(), req, snap) })
	explained := testing.AllocsPerRun(50, func() { k.Explain(context.Background(), req, snap) })
	if explained <= decided {
		t.Errorf("Decide allocates %v times per call and Explain %v; Decide pays for an explanation", decided, explained)
	}
}

// TestATypedNilRefusalIsStillARefusal: a refusal handed in as a nil
// *ValidationError inside the error is malformed input to Decide and to
// Explain alike, never a crash.
func TestATypedNilRefusalIsStillARefusal(t *testing.T) {
	var refused *contract.ValidationError
	req := request(readEnvelope())
	req.Refusal = refused
	for name, call := range map[string]func(*core.Kernel) core.Outcome{
		"Decide": func(k *core.Kernel) core.Outcome { return k.Decide(context.Background(), req, snapshot(t, allowReads)) },
		"Explain": func(k *core.Kernel) core.Outcome {
			out, _ := k.Explain(context.Background(), req, snapshot(t, allowReads))
			return out
		},
	} {
		t.Run(name, func(t *testing.T) {
			var out core.Outcome
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s panicked: %v", name, r)
					}
				}()
				out = call(kernelAt(t, options()))
			}()
			check(t, out, expect{verdictIndeterminate, core.Block, []string{codeMalformedInput}})
		})
	}
}
