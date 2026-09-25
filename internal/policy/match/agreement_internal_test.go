package match

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/reasons"
	"github.com/guardana/control/internal/policy/rules"
)

// Every program is compiled from what Parse returned, and Compile checks
// again, on its own copy, part of what Parse checks. The tests in this file
// run the real Parse into Compile, so the two copies cannot come to disagree
// without a failure here, before a signed bundle meets the disagreement at
// its first load.

// TestCompileAcceptsEveryDocumentParseAccepts: the sample documents, one that
// names the empty key, and documents drawn from the grammar. Whatever Parse
// accepts, Compile accepts.
func TestCompileAcceptsEveryDocumentParseAccepts(t *testing.T) {
	t.Run("the sample documents", func(t *testing.T) {
		dir := os.DirFS(filepath.Join("..", "..", "..", "testdata", "policy", "documents"))
		names, err := fs.Glob(dir, "*.json")
		if err != nil {
			t.Fatalf("listing the sample documents: %v", err)
		}
		for _, name := range []string{"every-field.json", "example.json"} {
			if !slices.Contains(names, name) {
				t.Fatalf("the sample documents are %q, without %s", names, name)
			}
		}
		for _, name := range names {
			raw, err := fs.ReadFile(dir, name)
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			parseThenCompile(t, name, raw)
		}
	})
	t.Run("the empty key", func(t *testing.T) {
		parseThenCompile(t, "the empty-key document", []byte(emptyKeyDocument))
	})
	t.Run("a veto", func(t *testing.T) {
		parseThenCompile(t, "the veto document", []byte(vetoDocument))
	})
	t.Run("drawn documents", testDrawnDocuments)
}

// emptyKeyDocument names the empty key in both map constraints, alone and
// beside another key. An envelope may carry that key.
const emptyKeyDocument = `{
  "apiVersion": "agent-policy/v1alpha1",
  "bundle": {"id": "payments", "version": "1", "serial": 1, "maxStaleSeconds": 300},
  "rules": [
    {"id": "unnamed-attribute", "effect": "REQUIRE_APPROVAL",
     "when": {"principal": {"attributes": {"": ["payments"]}}}},
    {"id": "unnamed-label", "effect": "DENY",
     "when": {"resource": {"labels": {"": ["gold"], "tier": ["gold"]}}}}
  ]
}`

// vetoDocument names the external constraint alone and beside a scope.
const vetoDocument = `{
  "apiVersion": "agent-policy/v1alpha1",
  "bundle": {"id": "payments", "version": "1", "serial": 1, "maxStaleSeconds": 300},
  "rules": [
    {"id": "refunds", "effect": "ALLOW", "when": {"action": {"name": ["refund"]}}},
    {"id": "refunds-vetoed", "effect": "DENY", "when": {"action": {"name": ["refund"]}, "external": {"denies": true}}},
    {"id": "all-vetoed", "effect": "DENY", "when": {"external": {"denies": true}}}
  ]
}`

// parseThenCompile requires Parse to accept raw, which the caller holds to be
// valid, and Compile to accept what Parse returned.
func parseThenCompile(t *testing.T, name string, raw []byte) {
	t.Helper()
	doc, _, err := rules.Parse(raw)
	if err != nil {
		t.Fatalf("Parse refused %s: %v", name, err)
	}
	if _, err := Compile(doc); err != nil {
		t.Errorf("Compile refused %s, which Parse accepted: %v", name, err)
	}
}

// testDrawnDocuments requires Compile to accept each drawn document that Parse
// accepts. The generator draws only what the grammar allows, so Parse accepts
// nearly all of them. Were it to accept fewer than half, the generator would
// have drifted from the grammar, and the property would hold of too little to
// mean anything.
func testDrawnDocuments(t *testing.T) {
	drawn, parsed := 0, 0
	rapid.Check(t, func(rt *rapid.T) {
		raw, err := json.Marshal(documentTree(rt))
		if err != nil {
			rt.Fatalf("rendering a drawn document: %v", err)
		}
		drawn++
		doc, _, err := rules.Parse(raw)
		if err != nil {
			return
		}
		parsed++
		if _, err := Compile(doc); err != nil {
			rt.Fatalf("Compile refused a document Parse accepted: %v\n%s", err, raw)
		}
	})
	if parsed*2 < drawn {
		t.Fatalf("Parse accepted %d of %d drawn documents", parsed, drawn)
	}
	t.Logf("Parse accepted %d of %d drawn documents, and Compile accepted each of those", parsed, drawn)
}

// TestParseAndCompileAcceptTheSameReasons crosses every registered code, three
// strings that are none, and no reason at all with the four rule effects. Each
// pair goes through the real Parse, and to Compile in the model a program
// built without Parse would carry. The two accept the same pairs, and a
// program from either gives the authored code, or the effect's own when none
// was authored.
func TestParseAndCompileAcceptTheSameReasons(t *testing.T) {
	// Each effect's own code, the one a rule carries when it names none.
	own := map[string]string{
		"ALLOW": "RULE_ALLOW", "DENY": "RULE_DENY",
		"REQUIRE_APPROVAL": "APPROVAL_REQUIRED", "ALLOW_WITH_OBLIGATIONS": "OBLIGATIONS_ATTACHED",
	}
	candidates := []string{"rule_deny", "RULE_DENY ", "NOT_A_CODE"}
	for _, code := range reasons.All() {
		candidates = append(candidates, code.ID)
	}
	accepted := 0
	for _, effect := range slices.Sorted(maps.Keys(own)) {
		if !agreeOnReason(t, reasonCase{effect: effect, want: own[effect]}) {
			t.Errorf("%s with no reason is refused", effect)
		}
		for _, reason := range candidates {
			if agreeOnReason(t, reasonCase{effect: effect, reason: reason, authored: true, want: reason}) {
				accepted++
			}
		}
	}
	// Both refusing everything would agree as well.
	pairs := 0
	for _, allowed := range authorableReasons() {
		pairs += len(allowed)
	}
	if accepted != pairs {
		t.Errorf("Parse and Compile both accept %d authored pairs, and the authorable list has %d", accepted, pairs)
	}
}

// reasonCase is one rule effect, as a document spells it, with one reason.
// authored false leaves the reason out of the document, and "" in the model.
type reasonCase struct {
	effect, reason string
	authored       bool
	want           string // the code a matched rule gives when both accept
}

func (c reasonCase) String() string {
	if !c.authored {
		return c.effect + " with no reason"
	}
	return fmt.Sprintf("%s with reason %q", c.effect, c.reason)
}

// document is the case as an author writes it: one rule matching a call named
// refund, with the obligation ALLOW_WITH_OBLIGATIONS cannot do without.
func (c reasonCase) document(t *testing.T) []byte {
	t.Helper()
	r := map[string]any{"id": "r", "effect": c.effect, "when": map[string]any{"action": map[string]any{"name": []string{"refund"}}}}
	if c.authored {
		r["reason"] = c.reason
	}
	if c.effect == "ALLOW_WITH_OBLIGATIONS" {
		r["obligations"] = []map[string]any{{"type": "read_only"}}
	}
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "agent-policy/v1alpha1",
		"bundle":     map[string]any{"id": "payments", "version": "1", "serial": 1, "maxStaleSeconds": 300},
		"rules":      []map[string]any{r},
	})
	if err != nil {
		t.Fatalf("rendering %s: %v", c, err)
	}
	return raw
}

// model is the same case built by hand.
func (c reasonCase) model() rules.Rule {
	r := rules.Rule{ID: "r", Effect: ruleEffects()[c.effect], When: refund()}
	if c.authored {
		r.Reason = c.reason
	}
	if r.Effect == obligationsEffect {
		r.Obligations = []rules.Obligation{{Type: "read_only"}}
	}
	return r
}

// agreeOnReason takes one case through Parse and through Compile, and reports
// whether both accepted it.
func agreeOnReason(t *testing.T, c reasonCase) bool {
	t.Helper()
	parsed, _, parseErr := rules.Parse(c.document(t))
	built, compileErr := Compile(&rules.Document{Rules: []rules.Rule{c.model()}})
	switch {
	case parseErr == nil && compileErr == nil:
		emitsTheCode(t, c, parsed, built)
		return true
	case parseErr != nil && compileErr != nil:
		refusedForTheReason(t, c, parseErr, compileErr)
		return false
	}
	t.Errorf("%s: Parse = %v and Compile = %v, which must agree", c, parseErr, compileErr)
	return false
}

// emitsTheCode compiles what Parse returned, and holds that program and the
// one built from the model to the code the case wants.
func emitsTheCode(t *testing.T, c reasonCase, parsed *rules.Document, built *Program) {
	t.Helper()
	fromParse, err := Compile(parsed)
	if err != nil {
		t.Errorf("%s: Compile refused what Parse returned: %v", c, err)
		return
	}
	env := &controlv1.ActionEnvelope{Action: &controlv1.Action{Name: "refund"}}
	for _, p := range []*Program{fromParse, built} {
		if got := p.Evaluate(env, Inputs{}).ReasonCodes; !slices.Equal(got, []string{c.want}) {
			t.Errorf("%s gives codes %q, want %s alone", c, got, c.want)
		}
	}
}

// refusedForTheReason holds both refusals to the reason: two refusals for
// other causes would agree by accident.
func refusedForTheReason(t *testing.T, c reasonCase, parseErr, compileErr error) {
	t.Helper()
	var pe *rules.Error
	if !errors.As(parseErr, &pe) || !errors.Is(parseErr, rules.ErrReason) || pe.Field != "rules[0].reason" {
		t.Errorf("%s: Parse refused it for another cause: %v", c, parseErr)
	}
	var ce *compileError
	if !errors.As(compileErr, &ce) || !errors.Is(compileErr, errReason) || ce.field != "reason" {
		t.Errorf("%s: Compile refused it for another cause: %v", c, compileErr)
	}
}

// The grammar as the generator below draws it.

// whenFields is each group of when with its fields, as a document spells them.
func whenFields() map[string][]string {
	return map[string][]string{
		"principal":   {"id", "type", "authnStrength", "tenantId", "attributes"},
		"agent":       {"id", "framework"},
		"action":      {"name", "provider", "protocol", "kind", "effect"},
		"resource":    {"type", "id", "tenantId", "environment", "labels"},
		"destination": {"trustZone", "host"},
		"data":        {"sensitivityAtLeast"},
		"flow":        {"toxicAtLeast"},
		"delegation":  {"scopes"},
	}
}

// ruleEffects is each rule effect as a document spells it.
func ruleEffects() map[string]controlv1.Verdict {
	return map[string]controlv1.Verdict{
		"ALLOW": allowEffect, "DENY": denyEffect,
		"REQUIRE_APPROVAL": approvalEffect, "ALLOW_WITH_OBLIGATIONS": obligationsEffect,
	}
}

// obligationCatalogue is the eleven obligation types, written out.
func obligationCatalogue() []string {
	return []string{
		"redact_fields", "read_only", "restrict_resources", "require_idempotency_key",
		"cap_amount", "cap_rate", "require_sandbox", "second_approver", "emit_alert",
		"shorten_timeout", "deny_external_sink",
	}
}

// mapKeys are keys Parse accepts in a map, no two of them folding together:
// the empty key, keys beyond ASCII and with a space inside, keys that hold
// "reserved" outside the reserved namespace, and 1024 bytes.
func mapKeys() []string {
	return []string{"", "team", "tier", "rôle", "k 1", "漢", "Reserved.x", "reserved", "x.reserved.y", strings.Repeat("k", 1024)}
}

// hostNames are drawn in lower case, with no dot at either end and no empty
// label, a spelling every rule for a host accepts.
func hostNames() []string {
	return []string{"paste.example.net", "ledger.internal", "a-1.b2", "xn--bcher-kva.example", "10.0.0.1", "::1"}
}

// enumNames is every name a document may spell a value of the enum with: each
// declared value but the zero one, without its prefix.
func enumNames(d protoreflect.EnumDescriptor, prefix string) []string {
	values := d.Values()
	out := make([]string, 0, values.Len())
	for i := range values.Len() {
		if v := values.Get(i); v.Number() != 0 {
			out = append(out, strings.TrimPrefix(string(v.Name()), prefix))
		}
	}
	return out
}

// identifierGen draws strings Parse reads as identifiers: never empty, no
// white space at either end, and quotes, backslashes, spaces and characters
// beyond ASCII inside; or one of a few chosen ones: 1024 bytes, an accent
// written as a combining mark, a path and a prefixed name.
func identifierGen() *rapid.Generator[string] {
	edge := rapid.SampledFrom([]rune(`abcXYZ019_-:/.é漢"\`))
	inner := rapid.SampledFrom([]rune(`abc 9_-é"\`))
	drawn := rapid.Custom(func(t *rapid.T) string {
		s := string(edge.Draw(t, "first"))
		if middle := rapid.SliceOfN(inner, 0, 5).Draw(t, "middle"); len(middle) > 0 {
			s += string(middle) + string(edge.Draw(t, "last"))
		}
		return s
	})
	chosen := rapid.SampledFrom([]string{strings.Repeat("v", 1024), "e\u0301quipe", "ledger/eu-1", "user:ana"})
	return rapid.OneOf(drawn, chosen)
}

// documentTree draws a document of the grammar as the tree an author writes:
// one to six rules with distinct ids, and a bundle within its bounds.
func documentTree(t *rapid.T) map[string]any {
	n := rapid.IntRange(1, 6).Draw(t, "rules")
	ids := rapid.SliceOfNDistinct(identifierGen(), n, n, rapid.ID[string]).Draw(t, "ids")
	rs := make([]map[string]any, n)
	for i, id := range ids {
		rs[i] = ruleTree(t, id)
	}
	return map[string]any{
		"apiVersion": "agent-policy/v1alpha1",
		"bundle": map[string]any{
			"id":              identifierGen().Draw(t, "bundle.id"),
			"version":         identifierGen().Draw(t, "bundle.version"),
			"serial":          rapid.Int64Range(1, 1<<53-1).Draw(t, "bundle.serial"),
			"maxStaleSeconds": rapid.Int64Range(1, math.MaxInt64/int64(time.Second)).Draw(t, "bundle.maxStaleSeconds"),
		},
		"rules": rs,
	}
}

// ruleTree draws a rule: an effect, a reason a rule of that effect may name or
// none, and the obligations it must or may carry.
func ruleTree(t *rapid.T, id string) map[string]any {
	effects := ruleEffects()
	effect := rapid.SampledFrom(slices.Sorted(maps.Keys(effects))).Draw(t, "effect")
	r := map[string]any{"id": id, "effect": effect, "when": whenTree(t)}
	if rapid.Bool().Draw(t, "authored") {
		r["reason"] = rapid.SampledFrom(authorableReasons()[effects[effect]]).Draw(t, "reason")
	}
	if effect == "ALLOW_WITH_OBLIGATIONS" || effect == "REQUIRE_APPROVAL" && rapid.Bool().Draw(t, "obliged") {
		r["obligations"] = obligationsTree(t)
	}
	if effect == "DENY" && rapid.Bool().Draw(t, "vetoes") {
		r["when"].(map[string]any)["external"] = map[string]any{"denies": true}
	}
	return r
}

// whenTree draws some of the groups, each with some of its fields.
func whenTree(t *rapid.T) map[string]any {
	fields := whenFields()
	when := map[string]any{}
	for _, group := range someOf(t, slices.Sorted(maps.Keys(fields)), "groups") {
		members := map[string]any{}
		for _, field := range someOf(t, fields[group], group) {
			members[field] = fieldTree(t, group+"."+field, field)
		}
		when[group] = members
	}
	return when
}

func someOf(t *rapid.T, names []string, label string) []string {
	return rapid.SliceOfNDistinct(rapid.SampledFrom(names), 1, len(names), rapid.ID[string]).Draw(t, label)
}

// fieldTree draws a field's value. Lists may repeat a value.
func fieldTree(t *rapid.T, label, field string) any {
	switch field {
	case "attributes", "labels":
		return mapTree(t, label, func(t *rapid.T, label string) any { return identifiers(t, label) })
	case "effect":
		return rapid.SliceOfN(rapid.SampledFrom(enumNames(controlv1.EffectClass(0).Descriptor(), "EFFECT_CLASS_")), 1, 3).Draw(t, label)
	case "trustZone":
		return rapid.SliceOfN(rapid.SampledFrom(enumNames(controlv1.TrustZone(0).Descriptor(), "TRUST_ZONE_")), 1, 3).Draw(t, label)
	case "sensitivityAtLeast", "toxicAtLeast":
		return rapid.SampledFrom(enumNames(controlv1.Sensitivity(0).Descriptor(), "SENSITIVITY_")).Draw(t, label)
	case "host":
		return rapid.SliceOfN(rapid.SampledFrom(hostNames()), 1, 3).Draw(t, label)
	}
	return identifiers(t, label)
}

func identifiers(t *rapid.T, label string) []string {
	return rapid.SliceOfN(identifierGen(), 1, 4).Draw(t, label)
}

// mapTree draws a map of one to three keys, each holding a value drawn by
// value.
func mapTree(t *rapid.T, label string, value func(*rapid.T, string) any) map[string]any {
	keys := rapid.SliceOfNDistinct(rapid.SampledFrom(mapKeys()), 1, 3, rapid.ID[string]).Draw(t, label+".keys")
	out := make(map[string]any, len(keys))
	for i, key := range keys {
		out[key] = value(t, label+"."+strconv.Itoa(i))
	}
	return out
}

// obligationsTree draws one to eight obligations from the catalogue, repeats
// allowed, each with or without parameters and the advisory flag.
func obligationsTree(t *rapid.T) []map[string]any {
	out := make([]map[string]any, rapid.IntRange(1, 8).Draw(t, "obligations"))
	for i := range out {
		o := map[string]any{"type": rapid.SampledFrom(obligationCatalogue()).Draw(t, "type")}
		if rapid.Bool().Draw(t, "parameters") {
			o["params"] = mapTree(t, "params", func(t *rapid.T, label string) any {
				return rapid.OneOf(identifierGen(), rapid.Just("")).Draw(t, label)
			})
		}
		if rapid.Bool().Draw(t, "flagged") {
			o["advisory"] = rapid.Bool().Draw(t, "advisory")
		}
		out[i] = o
	}
	return out
}
