package rules

import (
	"testing"
)

// refusalCase is a refused document and its accepting twin, which differs
// from it only in the place the case is about. The twin is what shows the
// refusal is the one named and not some other.
type refusalCase struct {
	name string
	doc  string
	twin string
	want refusal
}

func runRefusals(t *testing.T, cases []refusalCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expectAccepted(t, tc.twin)
			expectRefusal(t, tc.doc, tc.want)
		})
	}
}

func docWithAPIVersion(value string) string {
	return `{"apiVersion":` + value + `,"bundle":{"id":"p","version":"1","serial":1,"maxStaleSeconds":1},"rules":[` + denyRuleJSON + `]}`
}

func TestDocumentRefusals(t *testing.T) {
	t.Parallel()
	valid := docWithRules(denyRuleJSON)
	bundle := `"bundle":{"id":"p","version":"1","serial":1,"maxStaleSeconds":1}`
	rules := `"rules":[` + denyRuleJSON + `]`
	current := docWithAPIVersion(`"agent-policy/v1alpha1"`)
	runRefusals(t, []refusalCase{
		{"an array", `[]`, valid, refusal{ErrWrongType, "", ""}},
		{"a string", `"agent-policy/v1alpha1"`, valid, refusal{ErrWrongType, "", ""}},
		{"null", `null`, valid, refusal{ErrWrongType, "", ""}},
		{"an unknown key", `{"apiVersion":"agent-policy/v1alpha1",` + bundle + `,` + rules + `,"createdAt":1}`, valid, refusal{ErrUnknownKey, "", ""}},
		{"no apiVersion", `{` + bundle + `,` + rules + `}`, valid, refusal{ErrMissingKey, "apiVersion", ""}},
		{"no bundle", `{"apiVersion":"agent-policy/v1alpha1",` + rules + `}`, valid, refusal{ErrMissingKey, "bundle", ""}},
		{"no rules", `{"apiVersion":"agent-policy/v1alpha1",` + bundle + `}`, valid, refusal{ErrMissingKey, "rules", ""}},
		{"nothing", `{}`, valid, refusal{ErrMissingKey, "apiVersion", ""}},
		{"a later version", docWithAPIVersion(`"agent-policy/v1alpha2"`), current, refusal{ErrAPIVersion, "apiVersion", ""}},
		{"the major alone", docWithAPIVersion(`"agent-policy/v1"`), current, refusal{ErrAPIVersion, "apiVersion", ""}},
		{"upper case", docWithAPIVersion(`"AGENT-POLICY/V1ALPHA1"`), current, refusal{ErrAPIVersion, "apiVersion", ""}},
		{"a trailing space", docWithAPIVersion(`"agent-policy/v1alpha1 "`), current, refusal{ErrAPIVersion, "apiVersion", ""}},
		{"an empty version", docWithAPIVersion(`""`), current, refusal{ErrAPIVersion, "apiVersion", ""}},
		{"a number for the version", docWithAPIVersion(`1`), current, refusal{ErrWrongType, "apiVersion", ""}},
		{"null for the version", docWithAPIVersion(`null`), current, refusal{ErrWrongType, "apiVersion", ""}},
		{"rules an object", `{"apiVersion":"agent-policy/v1alpha1",` + bundle + `,"rules":{}}`, valid, refusal{ErrWrongType, "rules", ""}},
		{"no rule", `{"apiVersion":"agent-policy/v1alpha1",` + bundle + `,"rules":[]}`, valid, refusal{ErrEmpty, "rules", ""}},
		{"a rule that is a string", docWithRules(`"r"`), valid, refusal{ErrWrongType, "rules[0]", ""}},
	})
}

func TestBundleRefusals(t *testing.T) {
	t.Parallel()
	b := func(members string) string { return docWithBundle(`{` + members + `}`) }
	valid := b(`"id":"p","version":"1","serial":1,"maxStaleSeconds":1`)
	runRefusals(t, []refusalCase{
		{"an array", docWithBundle(`[]`), valid, refusal{ErrWrongType, "bundle", ""}},
		{"an unknown key", b(`"id":"p","version":"1","serial":1,"maxStaleSeconds":1,"digest":"x"`), valid, refusal{ErrUnknownKey, "bundle", ""}},
		{"a creation time", b(`"id":"p","version":"1","serial":1,"maxStaleSeconds":1,"createdAt":"2026-09-11T00:00:00Z"`), valid, refusal{ErrUnknownKey, "bundle", ""}},
		{"no id", b(`"version":"1","serial":1,"maxStaleSeconds":1`), valid, refusal{ErrMissingKey, "bundle.id", ""}},
		{"no version", b(`"id":"p","serial":1,"maxStaleSeconds":1`), valid, refusal{ErrMissingKey, "bundle.version", ""}},
		{"no serial", b(`"id":"p","version":"1","maxStaleSeconds":1`), valid, refusal{ErrMissingKey, "bundle.serial", ""}},
		{"no budget", b(`"id":"p","version":"1","serial":1`), valid, refusal{ErrMissingKey, "bundle.maxStaleSeconds", ""}},
		{"nothing", b(``), valid, refusal{ErrMissingKey, "bundle.id", ""}},
		{"serial zero", b(`"id":"p","version":"1","serial":0,"maxStaleSeconds":1`), valid, refusal{ErrNotPositive, "bundle.serial", ""}},
		{"serial negative", b(`"id":"p","version":"1","serial":-1,"maxStaleSeconds":1`), valid, refusal{ErrNotPositive, "bundle.serial", ""}},
		{"budget zero", b(`"id":"p","version":"1","serial":1,"maxStaleSeconds":0`), valid, refusal{ErrNotPositive, "bundle.maxStaleSeconds", ""}},
		{"budget negative", b(`"id":"p","version":"1","serial":1,"maxStaleSeconds":-300`), valid, refusal{ErrNotPositive, "bundle.maxStaleSeconds", ""}},
		{"serial a string", b(`"id":"p","version":"1","serial":"7","maxStaleSeconds":1`), valid, refusal{ErrWrongType, "bundle.serial", ""}},
		{"budget a boolean", b(`"id":"p","version":"1","serial":1,"maxStaleSeconds":true`), valid, refusal{ErrWrongType, "bundle.maxStaleSeconds", ""}},
		{"serial a float", b(`"id":"p","version":"1","serial":7.0,"maxStaleSeconds":1`), b(`"id":"p","version":"1","serial":7,"maxStaleSeconds":1`), refusal{ErrNotStrictJSON, "", ""}},
		{"serial with an exponent", b(`"id":"p","version":"1","serial":1e3,"maxStaleSeconds":1`), valid, refusal{ErrNotStrictJSON, "", ""}},
		{
			"serial past the JSON-safe range",
			b(`"id":"p","version":"1","serial":9007199254740992,"maxStaleSeconds":1`),
			b(`"id":"p","version":"1","serial":9007199254740991,"maxStaleSeconds":1`),
			refusal{ErrNotStrictJSON, "", ""},
		},
		{"id empty", b(`"id":"","version":"1","serial":1,"maxStaleSeconds":1`), valid, refusal{ErrEmpty, "bundle.id", ""}},
		{"id null", b(`"id":null,"version":"1","serial":1,"maxStaleSeconds":1`), valid, refusal{ErrWrongType, "bundle.id", ""}},
		{"version a number", b(`"id":"p","version":1,"serial":1,"maxStaleSeconds":1`), valid, refusal{ErrWrongType, "bundle.version", ""}},
	})
}

func TestRuleRefusals(t *testing.T) {
	t.Parallel()
	rule := func(members string) string { return docWithRules(`{` + members + `}`) }
	const when = `"when":{"action":{"name":["refund"]}}`
	valid := docWithRules(denyRuleJSON)
	other := `{"id":"s","effect":"ALLOW","when":{"action":{"name":["other"]}}}`
	runRefusals(t, []refusalCase{
		{"an unknown key", rule(`"id":"r","effect":"DENY","priority":1,` + when), valid, refusal{ErrUnknownKey, "rules[0]", "r"}},
		{"no id", rule(`"effect":"DENY",` + when), valid, refusal{ErrMissingKey, "rules[0].id", ""}},
		{"no effect", rule(`"id":"r",` + when), valid, refusal{ErrMissingKey, "rules[0].effect", "r"}},
		{"no when", rule(`"id":"r","effect":"DENY"`), valid, refusal{ErrMissingKey, "rules[0].when", "r"}},
		{"an empty id", rule(`"id":"","effect":"DENY",` + when), valid, refusal{ErrEmpty, "rules[0].id", ""}},
		{"an id that is a number", rule(`"id":7,"effect":"DENY",` + when), valid, refusal{ErrWrongType, "rules[0].id", ""}},
		{"an effect that is a list", rule(`"id":"r","effect":["DENY"],` + when), valid, refusal{ErrWrongType, "rules[0].effect", "r"}},
		{"a reason that is a number", rule(`"id":"r","effect":"DENY","reason":3,` + when), valid, refusal{ErrWrongType, "rules[0].reason", "r"}},
		{"a null reason", rule(`"id":"r","effect":"DENY","reason":null,` + when), valid, refusal{ErrWrongType, "rules[0].reason", "r"}},
		{"a null when", rule(`"id":"r","effect":"DENY","when":null`), valid, refusal{ErrWrongType, "rules[0].when", "r"}},
		{"a duplicate id", docWithRules(denyRuleJSON, `{"id":"r","effect":"ALLOW","when":{"action":{"name":["other"]}}}`), docWithRules(denyRuleJSON, other), refusal{ErrDuplicateRuleID, "rules[1].id", ""}},
		{"a duplicate two rules on", docWithRules(denyRuleJSON, other, `{"id":"r","effect":"ALLOW","when":{"action":{"name":["x"]}}}`), docWithRules(denyRuleJSON, other), refusal{ErrDuplicateRuleID, "rules[2].id", ""}},
	})
	// Rule ids compare exactly: two that differ only in case are two rules.
	expectAccepted(t, docWithRules(denyRuleJSON, `{"id":"R","effect":"ALLOW","when":{"action":{"name":["other"]}}}`))
}

func TestObligationRefusals(t *testing.T) {
	t.Parallel()
	rule := func(effect, obligations string) string {
		return docWithRules(`{"id":"r","effect":"` + effect + `"` + obligations + `,"when":{"action":{"name":["refund"]}}}`)
	}
	one := `,"obligations":[{"type":"cap_amount","params":{"max":"10"}}]`
	with := func(obligation string) string { return rule("REQUIRE_APPROVAL", `,"obligations":[`+obligation+`]`) }
	valid := with(`{"type":"read_only"}`)
	const at0 = "rules[0].obligations[0]"
	runRefusals(t, []refusalCase{
		{"on ALLOW", rule("ALLOW", one), rule("REQUIRE_APPROVAL", one), refusal{ErrObligationsOnEffect, "rules[0].obligations", "r"}},
		{"on DENY", rule("DENY", one), rule("ALLOW_WITH_OBLIGATIONS", one), refusal{ErrObligationsOnEffect, "rules[0].obligations", "r"}},
		{"an empty list on ALLOW", rule("ALLOW", `,"obligations":[]`), rule("ALLOW", ``), refusal{ErrObligationsOnEffect, "rules[0].obligations", "r"}},
		{"none with ALLOW_WITH_OBLIGATIONS", rule("ALLOW_WITH_OBLIGATIONS", ``), rule("ALLOW_WITH_OBLIGATIONS", one), refusal{ErrMissingKey, "rules[0].obligations", "r"}},
		{"an empty list with ALLOW_WITH_OBLIGATIONS", rule("ALLOW_WITH_OBLIGATIONS", `,"obligations":[]`), rule("ALLOW_WITH_OBLIGATIONS", one), refusal{ErrEmpty, "rules[0].obligations", "r"}},
		{"an empty list with REQUIRE_APPROVAL", rule("REQUIRE_APPROVAL", `,"obligations":[]`), rule("REQUIRE_APPROVAL", ``), refusal{ErrEmpty, "rules[0].obligations", "r"}},
		{"an object for the list", rule("REQUIRE_APPROVAL", `,"obligations":{"type":"read_only"}`), valid, refusal{ErrWrongType, "rules[0].obligations", "r"}},
		{"a string for an obligation", with(`"read_only"`), valid, refusal{ErrWrongType, at0, "r"}},
		{"an unknown key", with(`{"type":"read_only","until":1}`), valid, refusal{ErrUnknownKey, at0, "r"}},
		{"no type", with(`{"params":{"max":"10"}}`), with(`{"type":"cap_amount","params":{"max":"10"}}`), refusal{ErrMissingKey, at0 + ".type", "r"}},
		{"a type that is a number", with(`{"type":1}`), valid, refusal{ErrWrongType, at0 + ".type", "r"}},
		{"params a list", with(`{"type":"cap_amount","params":["max"]}`), valid, refusal{ErrWrongType, at0 + ".params", "r"}},
		{"params empty", with(`{"type":"cap_amount","params":{}}`), with(`{"type":"cap_amount"}`), refusal{ErrEmpty, at0 + ".params", "r"}},
		{"a param that is a number", with(`{"type":"cap_amount","params":{"max":10}}`), with(`{"type":"cap_amount","params":{"max":"10"}}`), refusal{ErrWrongType, at0 + ".params[0]", "r"}},
		{"a null param", with(`{"type":"cap_amount","params":{"a":"1","max":null}}`), with(`{"type":"cap_amount","params":{"a":"1","max":"1"}}`), refusal{ErrWrongType, at0 + ".params[1]", "r"}},
		{"advisory a string", with(`{"type":"read_only","advisory":"true"}`), with(`{"type":"read_only","advisory":true}`), refusal{ErrWrongType, at0 + ".advisory", "r"}},
		{"advisory null", with(`{"type":"read_only","advisory":null}`), valid, refusal{ErrWrongType, at0 + ".advisory", "r"}},
		{"the second one refused", with(`{"type":"read_only"},{"type":"nope"}`), with(`{"type":"read_only"},{"type":"cap_rate"}`), refusal{ErrObligationType, "rules[0].obligations[1].type", "r"}},
	})
}

// TestKeysMatchExactly: the model reads keys by exact comparison, never
// through a decoder that folds case, which would take each of these for the
// key it resembles. Two keys that fold together in one object are the
// canonical form's refusal.
func TestKeysMatchExactly(t *testing.T) {
	t.Parallel()
	kelvin, longS := string(rune(0x212A)), string(rune(0x017F))
	bundle := func(serialKey string) string {
		return docWithBundle(`{"id":"p","version":"1","` + serialKey + `":1,"maxStaleSeconds":1}`)
	}
	valid := docWithRules(denyRuleJSON)
	runRefusals(t, []refusalCase{
		{"apiVersion capitalised", `{"ApiVersion":"agent-policy/v1alpha1",` + headerJSON[len(`"apiVersion":"agent-policy/v1alpha1",`):] + `,"rules":[` + denyRuleJSON + `]}`, valid, refusal{ErrUnknownKey, "", ""}},
		{"serial capitalised", bundle("Serial"), bundle("serial"), refusal{ErrUnknownKey, "bundle", ""}},
		{"serial with a long s", bundle(longS + "erial"), bundle("serial"), refusal{ErrUnknownKey, "bundle", ""}},
		{"kind with a kelvin sign", docWithWhen(`{"action":{"` + kelvin + `ind":["x"]}}`), docWithWhen(`{"action":{"kind":["x"]}}`), refusal{ErrUnknownKey, "rules[0].when.action", "r"}},
		{"a group capitalised", docWithWhen(`{"Action":{"name":["x"]}}`), docWithWhen(`{"action":{"name":["x"]}}`), refusal{ErrUnknownKey, "rules[0].when", "r"}},
		{"a field capitalised", docWithWhen(`{"principal":{"tenantID":["x"]}}`), docWithWhen(`{"principal":{"tenantId":["x"]}}`), refusal{ErrUnknownKey, "rules[0].when.principal", "r"}},
		{"id capitalised", docWithRules(`{"ID":"r","effect":"DENY","when":{"action":{"name":["x"]}}}`), valid, refusal{ErrMissingKey, "rules[0].id", ""}},
		{"id twice, folded", docWithRules(`{"id":"r","ID":"s","effect":"DENY","when":{"action":{"name":["x"]}}}`), valid, refusal{ErrNotStrictJSON, "", ""}},
		{"a key twice", docWithWhen(`{"action":{"name":["x"],"name":["y"]}}`), valid, refusal{ErrNotStrictJSON, "", ""}},
		{"attribute keys that fold together", docWithWhen(`{"principal":{"attributes":{"Team":["a"],"team":["b"]}}}`), docWithWhen(`{"principal":{"attributes":{"Team":["a"],"tier":["b"]}}}`), refusal{ErrNotStrictJSON, "", ""}},
		{"param keys that fold together", docWithRules(`{"id":"r","effect":"REQUIRE_APPROVAL","obligations":[{"type":"cap_amount","params":{"max":"1","MAX":"2"}}],"when":{"action":{"name":["x"]}}}`), valid, refusal{ErrNotStrictJSON, "", ""}},
	})
}
