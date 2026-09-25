package core_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/pkg/contract"
)

// withDecisionPoint is options() with the decision point configured.
func withDecisionPoint() core.Options {
	opts := options()
	opts.DecisionPoint = decisionPoint
	return opts
}

// externalCase is one answer handed in beside a request, and what the
// decision says about it, written out.
type externalCase struct {
	answer   core.External
	want     expect
	ruleIDs  []string
	instance string
}

func checkExternal(t *testing.T, k *core.Kernel, env func() *controlv1.ActionEnvelope, rules []string, needs bool, cases map[string]externalCase) {
	t.Helper()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			req := request(env())
			req.External = c.answer
			out := decide(k, req, snapshot(t, rules...))
			check(t, out, c.want)
			d := out.Decision
			if out.NeedsExternal != needs {
				t.Errorf("NeedsExternal = %v, want %v", out.NeedsExternal, needs)
			}
			if !slices.Equal(d.GetPolicyRuleIds(), c.ruleIDs) {
				t.Errorf("PolicyRuleIds = %q, want %q", d.GetPolicyRuleIds(), c.ruleIDs)
			}
			if d.GetPdpInstance() != c.instance || d.GetPdpType() != "builtin" || d.GetPdpVersion() != "" {
				t.Errorf("pdp_type %q, pdp_instance %q, pdp_version %q; want builtin, %q and none",
					d.GetPdpType(), d.GetPdpInstance(), d.GetPdpVersion(), c.instance)
			}
		})
	}
}

// TestEveryAnswerOnAVetoedRead: a READ the bundle allows and vetoes when the
// decision point denies. Each answer gives its own verdict and code, and the
// decision names the decision point exactly when it consulted an answer.
func TestEveryAnswerOnAVetoedRead(t *testing.T) {
	both := []string{"allow-reads", "veto-reads"}
	undetermined := func(answer core.External, code string) externalCase {
		codes := []string{codeRuleAllow, codeRuleUndetermined}
		instance := ""
		if code != "" {
			codes, instance = append(codes, code), decisionPoint
		}
		return externalCase{answer, expect{verdictIndeterminate, core.Block, codes}, both, instance}
	}
	checkExternal(t, kernelAt(t, withDecisionPoint()), readEnvelope, []string{allowReads, vetoReads}, true, map[string]externalCase{
		"not asked": undetermined(core.External{}, ""),
		"allowed": {core.ExternalAllowed(), expect{verdictAllow, core.Execute, []string{codeRuleAllow, codePDPAllow}},
			[]string{"allow-reads"}, decisionPoint},
		"denied": {core.ExternalDenied(), expect{verdictDeny, core.Block, []string{codeRuleAllow, codeRuleDeny, codePDPDeny}},
			both, decisionPoint},
		"denied with obligations": {core.ExternalDeniedObligations(),
			expect{verdictDeny, core.Block, []string{codeRuleAllow, codeRuleDeny, codeObligationNotUnderstood}}, both, decisionPoint},
		"timeout":        undetermined(core.ExternalTimeout(), codePDPTimeout),
		"unavailable":    undetermined(core.ExternalUnavailable(), codePDPUnavailable),
		"answer refused": undetermined(core.ExternalAnswerRefused(), codePDPAnswerRefused),
	})
}

// TestAnAnswerNothingReads: a WRITE, which the veto rule does not reach. No
// answer changes the decision, none is named, and none is needed.
func TestAnAnswerNothingReads(t *testing.T) {
	cases := map[string]externalCase{}
	for _, a := range answers() {
		cases[a.name] = externalCase{a.external, expect{verdictAllow, core.Execute, []string{codeRuleAllow}}, []string{"allow-writes"}, ""}
	}
	checkExternal(t, kernelAt(t, withDecisionPoint()), writeEnvelope, []string{allowReads, allowWrites, vetoReads}, false, cases)
}

// TestTheDecisionPointIsNamedOnlyWhenConfigured: a consulted answer with no
// decision point configured leaves pdp_instance empty.
func TestTheDecisionPointIsNamedOnlyWhenConfigured(t *testing.T) {
	checkExternal(t, kernelAt(t, options()), readEnvelope, []string{allowReads, vetoReads}, true, map[string]externalCase{
		"allowed": {core.ExternalAllowed(), expect{verdictAllow, core.Execute, []string{codeRuleAllow, codePDPAllow}},
			[]string{"allow-reads"}, ""},
	})
}

// TestSilenceBlocksEveryEffectClass: silence is never an allow, whole:
// every declared effect class, a fresh and a stale snapshot, fail_open_read
// on and off, and every state that is no answer. Each blocks, with the
// answer needed and the read never opened. The stale READ under
// fail_open_read with an answer that does not deny runs, so the setting is
// live in this table and the block is the missing answer's.
func TestSilenceBlocksEveryEffectClass(t *testing.T) {
	everything := `{"id":"allow-everything","effect":"ALLOW","when":{"action":{"effect":["READ","WRITE","DELETE","EXECUTE","COMMUNICATE","TRANSACT","IDENTITY_OR_ACCESS","CONFIGURE","SPAWN_OR_DELEGATE"]}}}`
	values := controlv1.EffectClass(0).Descriptor().Values()
	blocked, opened := 0, 0
	for i := range values.Len() {
		effect := controlv1.EffectClass(values.Get(i).Number())
		if effect == controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED {
			continue
		}
		for _, stale := range []bool{false, true} {
			age := map[bool]time.Duration{false: 0, true: time.Hour}[stale]
			snap := snapshotAt(t, document(300, everything, vetoAll), at(-age))
			for _, failOpenRead := range []bool{false, true} {
				opts := withDecisionPoint()
				opts.FailOpenRead = failOpenRead
				k := kernelAt(t, opts)
				for _, a := range answers() {
					req := completeRequest(t, effect)
					req.External = a.external
					out := decide(k, req, snap)
					b, o := checkSilence(t, fmt.Sprintf("%s stale=%v fail-open=%v %s", effect, stale, failOpenRead, a.name), a, out)
					blocked, opened = blocked+b, opened+o
				}
			}
		}
	}
	if want := (values.Len() - 1) * 2 * 2 * 4; blocked != want || opened != 1 {
		t.Errorf("%d unanswered cases blocked, want %d; %d reads opened on an answer, want 1", blocked, want, opened)
	}
}

// checkSilence holds one outcome to the table above and reports whether it
// was an unanswered case that blocked, and whether it was a read the setting
// opened on an answer that does not deny.
func checkSilence(t *testing.T, name string, a answer, out core.Outcome) (blocked, opened int) {
	t.Helper()
	d := out.Decision
	if !out.NeedsExternal {
		t.Fatalf("%s: the veto was not needed; codes %q", name, d.GetReasonCodes())
	}
	marked := slices.Contains(d.GetReasonCodes(), codeFailOpenRead)
	switch {
	case a.unanswered && (out.Action != core.Block || marked || d.GetVerdict() != verdictIndeterminate):
		t.Errorf("%s: %s, action %d, codes %q", name, d.GetVerdict(), out.Action, d.GetReasonCodes())
	case a.unanswered:
		return 1, 0
	case marked && out.Action == core.Execute && a.external == core.ExternalAllowed():
		return 0, 1
	}
	return 0, 0
}

// TestNewHoldsTheDecisionPointToTheIdentifierRule: an identifier a decision
// could not carry is refused, one at the bound is not, and none at all is
// no decision point.
func TestNewHoldsTheDecisionPointToTheIdentifierRule(t *testing.T) {
	for name, c := range map[string]struct {
		id      string
		refused bool
	}{
		"none":                     {"", false},
		"an https identifier":      {decisionPoint, false},
		"at the bound":             {strings.Repeat("p", contract.MaxStringBytes), false},
		"over the bound":           {strings.Repeat("p", contract.MaxStringBytes+1), true},
		"a line feed inside":       {"https://pdp.example/\nx", true},
		"white space at the end":   {decisionPoint + " ", true},
		"a zero-width space":       {"https://pdp.example/" + string(rune(0x200B)), true},
		"invalid UTF-8":            {"https://pdp.example/\xff", true},
		"white space at the start": {" " + decisionPoint, true},
	} {
		opts := options()
		opts.DecisionPoint = c.id
		k, err := core.New(opts, fixedClock(base()), ids())
		switch {
		case c.refused && (!errors.Is(err, core.ErrDecisionPoint) || k != nil):
			t.Errorf("%s: New = (%v, %v), want ErrDecisionPoint and no kernel", name, k, err)
		case !c.refused && (err != nil || k == nil):
			t.Errorf("%s: New = (%v, %v), want a kernel", name, k, err)
		}
	}
}

// TestNewRefusesADecisionPointThatCouldCarryACredential: the identifier lands
// in evidence verbatim, so one with an '@', '?' or '#', where a URL can hide a
// credential, is refused; any other identifier the rule takes is carried.
func TestNewRefusesADecisionPointThatCouldCarryACredential(t *testing.T) {
	for name, c := range map[string]struct {
		id      string
		refused bool
	}{
		"https with a path":         {decisionPoint, false},
		"http with a port":          {"http://pdp.internal:8080/access/v1/evaluation", false},
		"a plain name":              {"corp-pdp", false},
		"a name with a colon":       {"pdp:primary", false},
		"userinfo with a password":  {"https://user:s3cret@pdp.example/access/v1/evaluation", true},
		"userinfo alone":            {"https://s3cret@pdp.example/access/v1/evaluation", true},
		"an at sign in a name":      {"pdp-s3cret@corp", true},
		"a query":                   {decisionPoint + "?api_key=s3cret", true},
		"an empty query":            {decisionPoint + "?", true},
		"a question mark in a name": {"pdp?s3cret", true},
		"a fragment":                {decisionPoint + "#s3cret", true},
		"a hash in a name":          {"pdp#s3cret", true},
	} {
		opts := options()
		opts.DecisionPoint = c.id
		k, err := core.New(opts, fixedClock(base()), ids())
		switch {
		case c.refused && (!errors.Is(err, core.ErrDecisionPoint) || k != nil):
			t.Errorf("%s: New = (%v, %v), want ErrDecisionPoint and no kernel", name, k, err)
		case c.refused && strings.Contains(err.Error(), "s3cret"):
			t.Errorf("%s: the refusal repeats the identifier: %v", name, err)
		case !c.refused && (err != nil || k == nil):
			t.Errorf("%s: New = (%v, %v), want a kernel", name, k, err)
		}
	}
}
