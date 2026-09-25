package rules

import (
	"slices"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/reasons"
)

// authorableWant is the reasons a rule of each effect may name, written out:
// each effect's default first, and three more for DENY. A kernel fact is on
// no list.
var authorableWant = map[controlv1.Verdict][]string{
	controlv1.Verdict_VERDICT_ALLOW:                  {"RULE_ALLOW"},
	controlv1.Verdict_VERDICT_DENY:                   {"RULE_DENY", "ENVIRONMENT_BOUNDARY", "OUT_OF_SCOPE_ACTION", "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL"},
	controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:       {"APPROVAL_REQUIRED"},
	controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS: {"OBLIGATIONS_ATTACHED"},
}

// effectNames is each rule effect as a document spells it.
var effectNames = map[controlv1.Verdict]string{
	controlv1.Verdict_VERDICT_ALLOW:                  "ALLOW",
	controlv1.Verdict_VERDICT_DENY:                   "DENY",
	controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:       "REQUIRE_APPROVAL",
	controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS: "ALLOW_WITH_OBLIGATIONS",
}

// catalogueWant is the eleven obligation types of the catalogue, written out.
var catalogueWant = []string{
	"redact_fields", "read_only", "restrict_resources", "require_idempotency_key",
	"cap_amount", "cap_rate", "require_sandbox", "second_approver", "emit_alert",
	"shorten_timeout", "deny_external_sink",
}

// ruleOfEffect is a document with one rule of that effect carrying extra
// members, and the one obligation ALLOW_WITH_OBLIGATIONS cannot do without.
func ruleOfEffect(effect, extra string) string {
	if effect == "ALLOW_WITH_OBLIGATIONS" {
		extra += `,"obligations":[{"type":"read_only"}]`
	}
	return docWithRules(`{"id":"r","effect":"` + effect + `"` + extra + `,"when":{"action":{"name":["refund"]}}}`)
}

// TestAuthorableReasonsAreRegisteredWithTheirEffect holds the literal list to
// the registry: each code exists, and its documented verdict is the effect of
// the rule that may name it, taken alone.
func TestAuthorableReasonsAreRegisteredWithTheirEffect(t *testing.T) {
	t.Parallel()
	for effect, codes := range authorableWant {
		for _, id := range codes {
			code, ok := reasons.Lookup(id)
			if !ok {
				t.Errorf("%s is not registered", id)
				continue
			}
			if code.Verdict != effect {
				t.Errorf("%s is documented with %v, authorable with %v", id, code.Verdict, effect)
			}
		}
		if got := reasonsFor(effect); !slices.Equal(got, codes) {
			t.Errorf("reasonsFor(%v) = %v, want %v", effect, got, codes)
		}
	}
	for _, effect := range []controlv1.Verdict{controlv1.Verdict_VERDICT_UNSPECIFIED, controlv1.Verdict_VERDICT_INDETERMINATE} {
		if got := reasonsFor(effect); got != nil {
			t.Errorf("reasonsFor(%v) = %v, want nothing", effect, got)
		}
	}
}

// TestParseAcceptsExactlyTheAuthorableReasons sweeps every registered code,
// and some that are not codes, against every rule effect.
func TestParseAcceptsExactlyTheAuthorableReasons(t *testing.T) {
	t.Parallel()
	candidates := []string{"", "rule_deny", "RULE_DENY ", "NOT_A_CODE"}
	for _, code := range reasons.All() {
		candidates = append(candidates, code.ID)
	}
	accepted := 0
	for effect, name := range effectNames {
		for _, id := range candidates {
			doc := ruleOfEffect(name, `,"reason":"`+id+`"`)
			if slices.Contains(authorableWant[effect], id) {
				expectAccepted(t, doc)
				accepted++
				continue
			}
			expectRefusal(t, doc, refusal{err: ErrReason, field: "rules[0].reason", rule: "r"})
		}
	}
	if accepted != 7 {
		t.Fatalf("%d effect and reason pairs accepted, want the 7 authorable ones", accepted)
	}
}

// TestDefaultReasonHasOneSpelling: written or left out, an effect's own
// default reads as "", and an authored reason is kept.
func TestDefaultReasonHasOneSpelling(t *testing.T) {
	t.Parallel()
	cases := []struct{ effect, extra, want string }{
		{"ALLOW", ``, ""},
		{"ALLOW", `,"reason":"RULE_ALLOW"`, ""},
		{"DENY", `,"reason":"RULE_DENY"`, ""},
		{"DENY", `,"reason":"OUT_OF_SCOPE_ACTION"`, "OUT_OF_SCOPE_ACTION"},
		{"REQUIRE_APPROVAL", `,"reason":"APPROVAL_REQUIRED"`, ""},
		{"ALLOW_WITH_OBLIGATIONS", `,"reason":"OBLIGATIONS_ATTACHED"`, ""},
	}
	for _, tc := range cases {
		if got := expectAccepted(t, ruleOfEffect(tc.effect, tc.extra)).Rules[0].Reason; got != tc.want {
			t.Errorf("%s with %q: Reason = %q, want %q", tc.effect, tc.extra, got, tc.want)
		}
	}
}

func TestObligationCatalogueIsTheElevenTypes(t *testing.T) {
	t.Parallel()
	got := obligationTypes()
	if len(got) != 11 || len(catalogueWant) != 11 {
		t.Fatalf("catalogue holds %d types, want 11", len(got))
	}
	for _, name := range catalogueWant {
		if !slices.Contains(got, name) {
			t.Errorf("catalogue lacks %s", name)
		}
		doc := ruleOfEffect("REQUIRE_APPROVAL", `,"obligations":[{"type":"`+name+`"}]`)
		if model := expectAccepted(t, doc); model.Rules[0].Obligations[0].Type != name {
			t.Errorf("type %s read as %q", name, model.Rules[0].Obligations[0].Type)
		}
	}
	for _, near := range []string{"READ_ONLY", "read-only", "readonly", "read_only ", " read_only", "", "cap_amounts", "redact"} {
		doc := ruleOfEffect("REQUIRE_APPROVAL", `,"obligations":[{"type":"`+near+`"}]`)
		expectRefusal(t, doc, refusal{err: ErrObligationType, field: "rules[0].obligations[0].type", rule: "r"})
	}
}

// TestKnownObligation: the accessor answers for one name, exactly as spelled,
// and for every name of the catalogue.
func TestKnownObligation(t *testing.T) {
	t.Parallel()
	for _, name := range catalogueWant {
		if !KnownObligation(name) {
			t.Errorf("KnownObligation(%q) = false, want true", name)
		}
	}
	for _, near := range []string{"", "READ_ONLY", "read-only", "readonly", "read_only ", " read_only", "cap_amounts", "redact", "reserved.read_only"} {
		if KnownObligation(near) {
			t.Errorf("KnownObligation(%q) = true, want false", near)
		}
	}
}
