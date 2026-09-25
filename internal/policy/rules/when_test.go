package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// groupFields is every constraint group of the document format and its keys,
// written out.
var groupFields = map[string][]string{
	"principal":   {"id", "type", "authnStrength", "tenantId", "attributes"},
	"agent":       {"id", "framework"},
	"action":      {"name", "provider", "protocol", "kind", "effect"},
	"resource":    {"type", "id", "tenantId", "environment", "labels"},
	"destination": {"trustZone", "host"},
	"data":        {"sensitivityAtLeast"},
	"flow":        {"toxicAtLeast"},
	"delegation":  {"scopes"},
	"external":    {"denies"},
}

// acceptedValue is a value the key accepts, as JSON.
func acceptedValue(key string) string {
	switch key {
	case "attributes", "labels":
		return `{"k":["v"]}`
	case "effect":
		return `["READ"]`
	case "trustZone":
		return `["PARTNER"]`
	case "sensitivityAtLeast", "toxicAtLeast":
		return `"SECRET"`
	case "denies":
		return `true`
	}
	return `["v"]`
}

func TestWhenRefusals(t *testing.T) {
	t.Parallel()
	valid := docWithWhen(`{"action":{"name":["x"]}}`)
	runRefusals(t, []refusalCase{
		{"an empty when", docWithWhen(`{}`), valid, refusal{ErrEmpty, "rules[0].when", "r"}},
		{"a list for when", docWithWhen(`[]`), valid, refusal{ErrWrongType, "rules[0].when", "r"}},
		{"an unknown group", docWithWhen(`{"action":{"name":["x"]},"context":{"x":["y"]}}`), valid, refusal{ErrUnknownKey, "rules[0].when", "r"}},
	})
}

// TestGroupRefusals: every group refuses an empty object, a value that is not
// an object and a key it does not have, and every key refuses null and a
// value of the wrong type. Each accepting twin carries the key alone, which
// also shows that every key of section 4 is read.
func TestGroupRefusals(t *testing.T) {
	t.Parallel()
	for group, keys := range groupFields {
		field := "rules[0].when." + group
		first := `"` + keys[0] + `":` + acceptedValue(keys[0])
		valid := docWithWhen(`{"` + group + `":{` + first + `}}`)
		runRefusals(t, []refusalCase{
			{group + " empty", docWithWhen(`{"` + group + `":{}}`), valid, refusal{ErrEmpty, field, "r"}},
			{group + " a list", docWithWhen(`{"` + group + `":[]}`), valid, refusal{ErrWrongType, field, "r"}},
			{group + " null", docWithWhen(`{"` + group + `":null}`), valid, refusal{ErrWrongType, field, "r"}},
			{group + " with an unknown key", docWithWhen(`{"` + group + `":{` + first + `,"zz":["v"]}}`), valid, refusal{ErrUnknownKey, field, "r"}},
		})
		for _, key := range keys {
			doc := func(value string) string { return docWithWhen(`{"` + group + `":{"` + key + `":` + value + `}}`) }
			runRefusals(t, []refusalCase{
				{field + "." + key + " null", doc(`null`), doc(acceptedValue(key)), refusal{ErrWrongType, field + "." + key, "r"}},
				{field + "." + key + " a number", doc(`1`), doc(acceptedValue(key)), refusal{ErrWrongType, field + "." + key, "r"}},
			})
		}
	}
}

func TestListRefusals(t *testing.T) {
	t.Parallel()
	const name = "rules[0].when.action.name"
	valid := docWithWhen(`{"action":{"name":["x"]}}`)
	runRefusals(t, []refusalCase{
		{"an empty list", docWithWhen(`{"action":{"name":[]}}`), valid, refusal{ErrEmpty, name, "r"}},
		{"a string for the list", docWithWhen(`{"action":{"name":"x"}}`), valid, refusal{ErrWrongType, name, "r"}},
		{"a number in the list", docWithWhen(`{"action":{"name":["x",1]}}`), docWithWhen(`{"action":{"name":["x","1"]}}`), refusal{ErrWrongType, name + "[1]", "r"}},
		{"null in the list", docWithWhen(`{"action":{"name":["x",null]}}`), valid, refusal{ErrWrongType, name + "[1]", "r"}},
		{"a list in the list", docWithWhen(`{"action":{"name":[["x"]]}}`), valid, refusal{ErrWrongType, name + "[0]", "r"}},
		{"an empty effect list", docWithWhen(`{"action":{"effect":[]}}`), docWithWhen(`{"action":{"effect":["READ"]}}`), refusal{ErrEmpty, "rules[0].when.action.effect", "r"}},
		{"a number for an effect", docWithWhen(`{"action":{"effect":[1]}}`), docWithWhen(`{"action":{"effect":["READ"]}}`), refusal{ErrWrongType, "rules[0].when.action.effect[0]", "r"}},
		{"an empty zone list", docWithWhen(`{"destination":{"trustZone":[]}}`), docWithWhen(`{"destination":{"trustZone":["PARTNER"]}}`), refusal{ErrEmpty, "rules[0].when.destination.trustZone", "r"}},
		{"an empty scope list", docWithWhen(`{"delegation":{"scopes":[]}}`), docWithWhen(`{"delegation":{"scopes":["s"]}}`), refusal{ErrEmpty, "rules[0].when.delegation.scopes", "r"}},
	})
}

func TestMapConstraintRefusals(t *testing.T) {
	t.Parallel()
	const attrs = "rules[0].when.principal.attributes"
	doc := func(attributes string) string { return docWithWhen(`{"principal":{"attributes":` + attributes + `}}`) }
	valid := doc(`{"a":["x"],"b":["y"]}`)
	runRefusals(t, []refusalCase{
		{"an empty map", doc(`{}`), valid, refusal{ErrEmpty, attrs, "r"}},
		{"a list for the map", doc(`[["x"]]`), valid, refusal{ErrWrongType, attrs, "r"}},
		{"a string for a value list", doc(`{"a":["x"],"b":"y"}`), valid, refusal{ErrWrongType, attrs + "[1]", "r"}},
		{"an empty value list", doc(`{"a":["x"],"b":[]}`), valid, refusal{ErrEmpty, attrs + "[1]", "r"}},
		{"null for a value list", doc(`{"a":null,"b":["y"]}`), valid, refusal{ErrWrongType, attrs + "[0]", "r"}},
		{"a number in a value list", doc(`{"a":["x"],"b":["y",2]}`), valid, refusal{ErrWrongType, attrs + "[1][1]", "r"}},
		{"an empty string in a value list", doc(`{"a":["x"],"b":[""]}`), valid, refusal{ErrEmpty, attrs + "[1][0]", "r"}},
		{"labels empty", docWithWhen(`{"resource":{"labels":{}}}`), docWithWhen(`{"resource":{"labels":{"k":["v"]}}}`), refusal{ErrEmpty, "rules[0].when.resource.labels", "r"}},
	})
	// An entry is named by its position in sorted key order, whatever order
	// the document wrote it in, and never by its key. Each Parse decodes a
	// fresh map, whose iteration order is random, so a parser that skips
	// the sort names the right position by chance half the time; twenty
	// rounds leave it no run to pass on.
	for range 20 {
		expectRefusal(t, doc(`{"b":["x"],"a":[]}`), refusal{ErrEmpty, attrs + "[0]", "r"})
		expectRefusal(t, doc(`{"b":[],"a":["x"]}`), refusal{ErrEmpty, attrs + "[1]", "r"})
	}
}

// TestExternalRefusals: the external group has one form, a denial on a DENY
// rule. Each other effect is refused with the group spelled exactly as the
// DENY twin that parses, and the value false, which would read as the
// decision point allowing, is refused on DENY as well.
func TestExternalRefusals(t *testing.T) {
	t.Parallel()
	const field = "rules[0].when.external"
	veto := `{"action":{"name":["refund"]},"external":{"denies":true}}`
	rule := func(effect, extra, when string) string {
		return docWithRules(`{"id":"r","effect":"` + effect + `"` + extra + `,"when":` + when + `}`)
	}
	twin := rule("DENY", "", veto)
	obliged := `,"obligations":[{"type":"read_only"}]`
	runRefusals(t, []refusalCase{
		{"on ALLOW", rule("ALLOW", "", veto), twin, refusal{ErrExternalEffect, field, "r"}},
		{"on REQUIRE_APPROVAL", rule("REQUIRE_APPROVAL", "", veto), twin, refusal{ErrExternalEffect, field, "r"}},
		{"on REQUIRE_APPROVAL with obligations", rule("REQUIRE_APPROVAL", obliged, veto), twin, refusal{ErrExternalEffect, field, "r"}},
		{"on ALLOW_WITH_OBLIGATIONS", rule("ALLOW_WITH_OBLIGATIONS", obliged, veto), twin, refusal{ErrExternalEffect, field, "r"}},
		{"alone on ALLOW", rule("ALLOW", "", `{"external":{"denies":true}}`), rule("DENY", "", `{"external":{"denies":true}}`),
			refusal{ErrExternalEffect, field, "r"}},
		{"denies false", rule("DENY", "", `{"action":{"name":["refund"]},"external":{"denies":false}}`), twin,
			refusal{ErrExternal, field + ".denies", "r"}},
		{"denies as a string", rule("DENY", "", `{"action":{"name":["refund"]},"external":{"denies":"true"}}`), twin,
			refusal{ErrWrongType, field + ".denies", "r"}},
		{"no denies", rule("DENY", "", `{"action":{"name":["refund"]},"external":{"allows":true}}`), twin,
			refusal{ErrUnknownKey, field, "r"}},
	})
}

// TestExternalModel: the one form reads as a group holding a denial, and a
// rule that does not name the group holds none.
func TestExternalModel(t *testing.T) {
	t.Parallel()
	doc := expectAccepted(t, docWithWhen(`{"external":{"denies":true}}`))
	if got := doc.Rules[0].When.External; got == nil || !got.Denies {
		t.Errorf("External = %+v, want a denial", got)
	}
	doc = expectAccepted(t, docWithWhen(`{"action":{"name":["refund"]}}`))
	if got := doc.Rules[0].When.External; got != nil {
		t.Errorf("External = %+v on a rule that does not name it", got)
	}
}

// TestTheRefusedSampleDocument is the sample bundle that grants on the
// decision point's word: its second rule reads external on ALLOW. The same
// document with that rule's effect turned to DENY parses.
func TestTheRefusedSampleDocument(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "policy", "refused", "external-on-allow.json"))
	if err != nil {
		t.Fatal(err)
	}
	const granted = `"id": "refunds-granted", "effect": "ALLOW"`
	if !strings.Contains(string(raw), granted) {
		t.Fatalf("the sample no longer holds %s", granted)
	}
	expectRefusal(t, string(raw), refusal{ErrExternalEffect, "rules[1].when.external", "refunds-granted"})
	expectAccepted(t, strings.Replace(string(raw), granted, `"id": "refunds-granted", "effect": "DENY"`, 1))
}
