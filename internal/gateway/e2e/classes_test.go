package e2e_test

import (
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// declaredClasses is how many effect classes the frozen contract declares
// besides the unspecified one.
const declaredClasses = 9

const codeRequiredFieldAbsent = "REQUIRED_FIELD_ABSENT"

// answerCase is one cause the decision point can give, the code the decision
// carries for it, whether the call runs, and whether the decision consulted
// the decision point at all.
type answerCase struct {
	answer    string
	code      string
	runs      bool
	consulted bool
}

var everyAnswer = []answerCase{
	{answerAllow, codePDPAllow, true, true},
	{answerDeny, codePDPDeny, false, true},
	{answerObligations, codeObligationsUnmet, false, true},
	{answerSilent, codePDPTimeout, false, true},
	{answer500, codePDPUnavailable, false, true},
	{answerUnechoed, codePDPAnswerRefused, false, true},
}

// classRules allow class and let the decision point veto it.
func classRules(class controlv1.EffectClass) []string {
	c := effectSpelling(class)
	return []string{
		`{"id":"allow-` + c + `","effect":"ALLOW","when":{"action":{"effect":["` + c + `"]}}}`,
		`{"id":"veto-` + c + `","effect":"DENY","when":{"action":{"effect":["` + c + `"]},"external":{"denies":true}}}`,
	}
}

// expected is what a call of class does under a. A spawn or delegate call
// needs a delegation chain, which the MCP adapter never carries, so it is
// refused before the policy and the decision point see it, whatever the
// decision point would have said.
func expected(class controlv1.EffectClass, a answerCase) answerCase {
	if class == controlv1.EffectClass_EFFECT_CLASS_SPAWN_OR_DELEGATE {
		return answerCase{answer: a.answer, code: codeRequiredFieldAbsent}
	}
	return a
}

// TestEveryEffectClassUnderEveryAnswer drives a call of every effect class the
// contract declares through the real AuthZEN adapter under ENFORCE, one plane
// per class: an allow runs it once, and a denial, a denial with obligations,
// a timeout, a 500 and an answer without the request's identifier each block
// it, on a trail that validates, carries the answer's code and names the
// decision point. A spawn or delegate call is blocked unasked, as expected
// says.
func TestEveryEffectClassUnderEveryAnswer(t *testing.T) {
	classes := declaredEffectClasses()
	if len(classes) != declaredClasses {
		t.Fatalf("the contract declares %d effect classes, want %d: %v", len(classes), declaredClasses, classes)
	}
	for _, class := range classes {
		t.Run(effectSpelling(class), func(t *testing.T) {
			dp := newAuthZENDouble(t, answerAllow)
			p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce,
				rules: classRules(class), decisionPoint: dp.client(t)})
			agent := p.connect(t, "agent-a")
			for i, a := range everyAnswer {
				t.Run(a.answer, func(t *testing.T) {
					dp.set(a.answer)
					askAndExpect(t, p, dp, agent, classTool(class), i, expected(class, a))
				})
			}
		})
	}
}

// askAndExpect makes the plane's i-th call, to tool, and checks it ran or was
// blocked as want says, on a trail of its own that validates and carries
// want's code.
func askAndExpect(t *testing.T, p *plane, dp *authzenDouble, agent *sdk.ClientSession, tool string, i int, want answerCase) {
	t.Helper()
	ranBefore := p.victim.count(tool)
	res := call(t, agent, tool, map[string]any{"path": "/x"})
	order, byRequest := p.trails()
	if len(order) != i+1 {
		t.Fatalf("the plane recorded %d trails after call %d, want %d", len(order), i+1, i+1)
	}
	trail := byRequest[order[i]]
	if want.runs {
		if res.IsError {
			t.Fatalf("an allowed call came back as a refusal: %+v", res.StructuredContent)
		}
		expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
	} else {
		blockedWith(t, res, want.code)
		expectTrail(t, trail, kindProposed, kindDecided, kindBlocked)
	}
	if n, wantRan := p.victim.count(tool)-ranBefore, map[bool]int{true: 1, false: 0}[want.runs]; n != wantRan {
		t.Errorf("the upstream ran %d time(s), want %d", n, wantRan)
	}
	decision := eventOf(t, trail, kindDecided).GetDecision()
	identifier, asked := "", dp.questions()
	if want.consulted {
		identifier = dp.url
		if len(asked) != i+1 || asked[i] != order[i] {
			t.Errorf("the decision point was asked about %q, want the trail's request %s as question %d", asked, order[i], i+1)
		}
	} else if len(asked) != 0 {
		t.Errorf("the decision point was asked about %q, want nothing", asked)
	}
	expectConsulted(t, decision, want.code, identifier)
	if allows := decision.GetVerdict() == controlv1.Verdict_VERDICT_ALLOW; allows != want.runs {
		t.Errorf("the recorded verdict is %v on a call that runs=%t", decision.GetVerdict(), want.runs)
	}
}
