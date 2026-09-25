package rules

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// The spellings of section 4, written out, for the generators and for reading
// a model back into the tree it came from.
var (
	classByName = map[string]controlv1.EffectClass{
		"READ": controlv1.EffectClass_EFFECT_CLASS_READ, "WRITE": controlv1.EffectClass_EFFECT_CLASS_WRITE,
		"DELETE": controlv1.EffectClass_EFFECT_CLASS_DELETE, "EXECUTE": controlv1.EffectClass_EFFECT_CLASS_EXECUTE,
		"COMMUNICATE": controlv1.EffectClass_EFFECT_CLASS_COMMUNICATE, "TRANSACT": controlv1.EffectClass_EFFECT_CLASS_TRANSACT,
		"IDENTITY_OR_ACCESS": controlv1.EffectClass_EFFECT_CLASS_IDENTITY_OR_ACCESS,
		"CONFIGURE":          controlv1.EffectClass_EFFECT_CLASS_CONFIGURE,
		"SPAWN_OR_DELEGATE":  controlv1.EffectClass_EFFECT_CLASS_SPAWN_OR_DELEGATE,
	}
	zoneByName = map[string]controlv1.TrustZone{
		"TRUSTED_INTERNAL": controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL, "PARTNER": controlv1.TrustZone_TRUST_ZONE_PARTNER,
		"UNTRUSTED_EXTERNAL": controlv1.TrustZone_TRUST_ZONE_UNTRUSTED_EXTERNAL,
		"USER_CONTROLLED":    controlv1.TrustZone_TRUST_ZONE_USER_CONTROLLED,
		"MODEL_GENERATED":    controlv1.TrustZone_TRUST_ZONE_MODEL_GENERATED,
	}
	floorByName = map[string]controlv1.Sensitivity{
		"PUBLIC": controlv1.Sensitivity_SENSITIVITY_PUBLIC, "INTERNAL": controlv1.Sensitivity_SENSITIVITY_INTERNAL,
		"CONFIDENTIAL": controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL, "RESTRICTED": controlv1.Sensitivity_SENSITIVITY_RESTRICTED,
		"SECRET": controlv1.Sensitivity_SENSITIVITY_SECRET,
	}
)

// TestParseAcceptsGeneratedDocuments draws documents section 4 accepts and
// renders each twice, with members in any order, white space between tokens
// and characters escaped or not. Both renderings give the canonical form of
// the drawn tree, which parses to itself; all three give one model; and that
// model, read back into a tree, is the tree that was drawn, each default
// reason and each false advisory flag left out.
func TestParseAcceptsGeneratedDocuments(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		tree := documentGen().Draw(t, "document")
		first, second := render(t, tree), render(t, tree)
		want, err := canon.Canonicalize(tree)
		if err != nil {
			t.Fatalf("the drawn tree has no canonical form: %v", err)
		}
		model, canonical, err := Parse(first)
		if err != nil {
			t.Fatalf("Parse refused a document section 4 accepts: %v\n%s", err, first)
		}
		if !bytes.Equal(canonical, want) {
			t.Fatalf("canonical bytes\n got %s\nwant %s", canonical, want)
		}
		for _, input := range [][]byte{second, canonical} {
			again, bytesAgain, err := Parse(input)
			if err != nil || !bytes.Equal(bytesAgain, want) || !reflect.DeepEqual(again, model) {
				t.Fatalf("%s parses to another result: %v", input, err)
			}
		}
		if problem := modelProblem(model); problem != "" {
			t.Fatalf("the model %s", problem)
		}
		normalize(tree)
		if got := treeOf(model); !reflect.DeepEqual(got, tree) {
			t.Fatalf("model read back\n got %#v\nwant %#v", got, tree)
		}
	})
}

// TestParseRefusesGeneratedMutations breaks a drawn document in one place and
// expects the refusal the mutation declares, whatever else the document holds.
func TestParseRefusesGeneratedMutations(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		doc := documentGen().Draw(t, "document")
		m := rapid.SampledFrom(mutations()).Draw(t, "mutation")
		m.apply(t, doc)
		raw := render(t, doc)
		model, canonical, err := Parse(raw)
		if !errors.Is(err, m.want) || model != nil || canonical != nil {
			t.Fatalf("%s: Parse = %v, want %v\n%s", m.name, err, m.want, raw)
		}
	})
}

func sortedNames[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func anys[S ~[]E, E any](s S) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// Identifiers hold quotes, backslashes and characters outside ASCII, which
// the rendering escapes, and never white space at either end.
func identifierGen() *rapid.Generator[string] {
	edge := rapid.SampledFrom([]rune("abcXYZ019_-:/.é漢\"\\"))
	inner := rapid.SampledFrom([]rune("abc 9_-é\"\\"))
	return rapid.Custom(func(t *rapid.T) string {
		s := string(edge.Draw(t, "first"))
		middle := rapid.SliceOfN(inner, 0, 5).Draw(t, "middle")
		if len(middle) > 0 {
			s += string(middle) + string(edge.Draw(t, "last"))
		}
		return s
	})
}

func valuesGen() *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any { return anys(rapid.SliceOfN(identifierGen(), 1, 4).Draw(t, "values")) })
}

func namesGen(names []string) *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any { return anys(rapid.SliceOfN(rapid.SampledFrom(names), 1, 3).Draw(t, "names")) })
}

func mapGen(value *rapid.Generator[any]) *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any {
		out := map[string]any{}
		for k, v := range rapid.MapOfN(identifierGen(), value, 1, 3).Draw(t, "entries") {
			out[k] = v
		}
		return out
	})
}

// hostGen draws hosts with the one spelling a host has: lower-case labels of
// [a-z0-9-] joined by dots, or an IPv6 literal.
func hostGen() *rapid.Generator[any] {
	label := rapid.StringMatching(`[a-z0-9-]{1,4}`)
	name := rapid.Custom(func(t *rapid.T) string {
		return strings.Join(rapid.SliceOfN(label, 1, 3).Draw(t, "labels"), ".")
	})
	host := rapid.OneOf(name, rapid.SampledFrom([]string{"::1", "::ffff:1.2.3.4", "2001:db8::1"}))
	return rapid.Custom(func(t *rapid.T) any { return anys(rapid.SliceOfN(host, 1, 3).Draw(t, "hosts")) })
}

func fieldGen(key string) *rapid.Generator[any] {
	switch key {
	case "attributes", "labels":
		return mapGen(valuesGen())
	case "host":
		return hostGen()
	case "effect":
		return namesGen(sortedNames(classByName))
	case "trustZone":
		return namesGen(sortedNames(zoneByName))
	case "sensitivityAtLeast", "toxicAtLeast":
		return rapid.Custom(func(t *rapid.T) any { return rapid.SampledFrom(sortedNames(floorByName)).Draw(t, "floor") })
	}
	return valuesGen()
}

// someOf draws a non-empty subset of names.
func someOf(t *rapid.T, names []string, label string) []string {
	var out []string
	for _, n := range names {
		if rapid.Bool().Draw(t, label+"."+n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = append(out, rapid.SampledFrom(names).Draw(t, label))
	}
	return out
}

// whenGen draws the groups every effect may name; ruleGen adds external,
// which only a DENY rule may.
func whenGen(t *rapid.T) map[string]any {
	when := map[string]any{}
	groups := slices.DeleteFunc(sortedNames(groupFields), func(g string) bool { return g == "external" })
	for _, g := range someOf(t, groups, "groups") {
		group := map[string]any{}
		for _, k := range someOf(t, groupFields[g], g) {
			group[k] = fieldGen(k).Draw(t, g+"."+k)
		}
		when[g] = group
	}
	return when
}

func obligationsGen(t *rapid.T) []any {
	param := rapid.OneOf(rapid.Custom(func(t *rapid.T) any { return identifierGen().Draw(t, "p") }), rapid.Just[any](""))
	out := make([]any, rapid.IntRange(1, 8).Draw(t, "obligations"))
	for i := range out {
		o := map[string]any{"type": rapid.SampledFrom(catalogueWant).Draw(t, "type")}
		if rapid.Bool().Draw(t, "has params") {
			o["params"] = mapGen(param).Draw(t, "params")
		}
		if rapid.Bool().Draw(t, "has advisory") {
			o["advisory"] = rapid.Bool().Draw(t, "advisory")
		}
		out[i] = o
	}
	return out
}

func ruleGen(t *rapid.T, id string) map[string]any {
	effect := rapid.SampledFrom(sortedNames(effectByName())).Draw(t, "effect")
	rule := map[string]any{"id": id, "effect": effect, "when": whenGen(t)}
	if effect == "DENY" && rapid.Bool().Draw(t, "vetoes") {
		rule["when"].(map[string]any)["external"] = map[string]any{"denies": true}
	}
	if rapid.Bool().Draw(t, "has reason") {
		rule["reason"] = rapid.SampledFrom(authorableWant[effectByName()[effect]]).Draw(t, "reason")
	}
	if effect == "ALLOW_WITH_OBLIGATIONS" || (effect == "REQUIRE_APPROVAL" && rapid.Bool().Draw(t, "has obligations")) {
		rule["obligations"] = obligationsGen(t)
	}
	return rule
}

func documentGen() *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		n := rapid.IntRange(1, 5).Draw(t, "rules")
		ids := rapid.SliceOfNDistinct(identifierGen(), n, n, rapid.ID[string]).Draw(t, "ids")
		rules := make([]any, n)
		for i := range rules {
			rules[i] = ruleGen(t, ids[i])
		}
		return map[string]any{
			"apiVersion": "agent-policy/v1alpha1",
			"bundle": map[string]any{
				"id": identifierGen().Draw(t, "bundle id"), "version": identifierGen().Draw(t, "version"),
				"serial":          rapid.Int64Range(1, 1<<53-1).Draw(t, "serial"),
				"maxStaleSeconds": rapid.Int64Range(1, 9223372036).Draw(t, "budget"),
			},
			"rules": rules,
		}
	})
}

func effectByName() map[string]controlv1.Verdict {
	out := map[string]controlv1.Verdict{}
	for v, name := range effectNames {
		out[name] = v
	}
	return out
}

// render writes a tree as an author might: members in any order, white space
// between tokens, and each character escaped or literal.
func render(t *rapid.T, v any) []byte {
	var b bytes.Buffer
	writeValue(t, &b, v)
	return b.Bytes()
}

func writeValue(t *rapid.T, b *bytes.Buffer, v any) {
	b.WriteString(rapid.StringOfN(rapid.SampledFrom([]rune(" \t\n\r")), 0, 2, -1).Draw(t, "space"))
	switch x := v.(type) {
	case map[string]any:
		b.WriteByte('{')
		for i, k := range rapid.Permutation(sortedNames(x)).Draw(t, "order") {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(t, b, k)
			b.WriteByte(':')
			writeValue(t, b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeValue(t, b, item)
		}
		b.WriteByte(']')
	case string:
		writeString(t, b, x)
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	default:
		t.Fatalf("render: %T", v)
	}
	b.WriteString(rapid.StringOfN(rapid.SampledFrom([]rune(" \t\n\r")), 0, 2, -1).Draw(t, "space"))
}

func writeString(t *rapid.T, b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x10000 && rapid.IntRange(0, 3).Draw(t, "escape") == 0:
			fmt.Fprintf(b, "%cu%04x", '\\', r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// normalize leaves out of a drawn tree what the model does not hold apart:
// an effect's default reason, written out, and a false advisory flag.
func normalize(doc map[string]any) {
	for _, r := range doc["rules"].([]any) {
		rule := r.(map[string]any)
		effect := effectByName()[rule["effect"].(string)]
		if rule["reason"] == authorableWant[effect][0] {
			delete(rule, "reason")
		}
		obligations, _ := rule["obligations"].([]any)
		for _, o := range obligations {
			if o.(map[string]any)["advisory"] == false {
				delete(o.(map[string]any), "advisory")
			}
		}
	}
}
