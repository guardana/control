package core_test

import (
	"slices"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/pkg/contract"
)

// allowOrder allows a READ of order ord-1 alone, so a call that names no
// resource id leaves it unknown.
const allowOrder = `{"id":"allow-order","effect":"ALLOW","when":{"action":{"effect":["READ"]},"resource":{"id":["ord-1"]}}}`

// TestAnUnknownAllowReachesTheOutcome: the ALLOW rules whose match is unknown
// travel on the outcome and nowhere on the decision, which stays the DENY of
// no matching rule; a call that names the id is decided by it.
func TestAnUnknownAllowReachesTheOutcome(t *testing.T) {
	withID := func(id string) func() *controlv1.ActionEnvelope {
		return func() *controlv1.ActionEnvelope {
			env := readEnvelope()
			env.Resource.Id = id
			return env
		}
	}
	for name, c := range map[string]struct {
		env     func() *controlv1.ActionEnvelope
		refusal error
		want    expect
		ruleIDs []string
		unknown []string
	}{
		"no resource id": {env: withID(""), want: expect{verdictDeny, core.Block, []string{codeNoMatchingRule}},
			unknown: []string{"allow-order"}},
		"the allowed id": {env: withID("ord-1"), want: expect{verdictAllow, core.Execute, []string{codeRuleAllow}},
			ruleIDs: []string{"allow-order"}},
		"another id": {env: withID("ord-2"), want: expect{verdictDeny, core.Block, []string{codeNoMatchingRule}}},
		"a refused request": {env: withID(""), refusal: &contract.ValidationError{Field: "action.kind", Err: contract.ErrInvalidValue},
			want: expect{verdictIndeterminate, core.Block, []string{codeInvalidFieldValue}}},
	} {
		t.Run(name, func(t *testing.T) {
			req := request(c.env())
			req.Refusal = c.refusal
			out := decide(kernelAt(t, options()), req, snapshot(t, allowOrder))
			check(t, out, c.want)
			if !slices.Equal(out.UnknownAllows, c.unknown) {
				t.Errorf("UnknownAllows = %q, want %q", out.UnknownAllows, c.unknown)
			}
			if got := out.Decision.GetPolicyRuleIds(); !slices.Equal(got, c.ruleIDs) {
				t.Errorf("PolicyRuleIds = %q, want %q", got, c.ruleIDs)
			}
		})
	}
}
