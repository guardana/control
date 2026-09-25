package rules

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// mutation breaks a drawn document in one place and names the refusal that
// place gets. Each one always finds a place, so no draw passes vacuously.
type mutation struct {
	name  string
	want  error
	apply func(t *rapid.T, doc map[string]any)
}

func mutations() []mutation {
	return []mutation{
		{"an unknown key", ErrUnknownKey, func(t *rapid.T, doc map[string]any) {
			objects := schemaObjects(doc)
			objects[rapid.IntRange(0, len(objects)-1).Draw(t, "object")]["zz"] = "x"
		}},
		{"an empty list", ErrEmpty, func(t *rapid.T, doc map[string]any) { pickList(t, doc).set([]any{}) }},
		{"an empty identifier", ErrEmpty, func(t *rapid.T, doc map[string]any) { pickIdentifier(t, doc).set("") }},
		{"the identifier rule", ErrIdentifier, func(t *rapid.T, doc map[string]any) {
			s := pickIdentifier(t, doc)
			s.set(s.get() + zeroWidthSpace)
		}},
		{"a fifth effect", ErrEnum, func(t *rapid.T, doc map[string]any) {
			pickRule(t, doc)["effect"] = rapid.SampledFrom([]string{"INDETERMINATE", "UNSPECIFIED", "VERDICT_DENY", "deny"}).Draw(t, "effect")
		}},
		{"a rule id twice", ErrDuplicateRuleID, func(t *rapid.T, doc map[string]any) {
			doc["rules"] = append(doc["rules"].([]any), maps.Clone(pickRule(t, doc)))
		}},
		{"a serial or budget not above zero", ErrNotPositive, func(t *rapid.T, doc map[string]any) {
			key := rapid.SampledFrom([]string{"serial", "maxStaleSeconds"}).Draw(t, "key")
			doc["bundle"].(map[string]any)[key] = int64(rapid.IntRange(-5, 0).Draw(t, "value"))
		}},
		{"obligations on ALLOW or DENY", ErrObligationsOnEffect, func(t *rapid.T, doc map[string]any) {
			rule := pickRule(t, doc)
			rule["effect"] = rapid.SampledFrom([]string{"ALLOW", "DENY"}).Draw(t, "effect")
			delete(rule, "reason")
			rule["obligations"] = []any{map[string]any{"type": "read_only"}}
		}},
		{"65 values", ErrTooLarge, func(t *rapid.T, doc map[string]any) {
			s := pickList(t, doc)
			list := s.get()
			for len(list) < 65 {
				list = append(list, list[0])
			}
			s.set(list)
		}},
		{"a missing key", ErrMissingKey, func(t *rapid.T, doc map[string]any) {
			switch rapid.IntRange(0, 2).Draw(t, "where") {
			case 0:
				delete(doc, rapid.SampledFrom([]string{"apiVersion", "bundle", "rules"}).Draw(t, "key"))
			case 1:
				delete(doc["bundle"].(map[string]any), rapid.SampledFrom([]string{"id", "version", "serial", "maxStaleSeconds"}).Draw(t, "key"))
			default:
				delete(pickRule(t, doc), rapid.SampledFrom([]string{"id", "effect", "when"}).Draw(t, "key"))
			}
		}},
		{"a type outside the catalogue", ErrObligationType, func(t *rapid.T, doc map[string]any) {
			rule := pickRule(t, doc)
			rule["effect"] = "REQUIRE_APPROVAL"
			delete(rule, "reason")
			rule["obligations"] = []any{map[string]any{"type": rapid.SampledFrom([]string{"nope", "READ_ONLY", "read-only"}).Draw(t, "type")}}
		}},
		{"a reason the effect may not name", ErrReason, func(t *rapid.T, doc map[string]any) {
			rule := pickRule(t, doc)
			effect := effectByName()[rule["effect"].(string)]
			others := []string{"TENANT_MISMATCH", "POLICY_STALE", "DELEGATION_EXPIRED", "RULE_UNDETERMINED"}
			for e, codes := range authorableWant {
				if e != effect {
					others = append(others, codes...)
				}
			}
			slices.Sort(others)
			rule["reason"] = rapid.SampledFrom(others).Draw(t, "reason")
		}},
		{"an unset floor", ErrFloor, func(t *rapid.T, doc map[string]any) {
			pickRule(t, doc)["when"].(map[string]any)["data"] = map[string]any{"sensitivityAtLeast": "UNSPECIFIED"}
		}},
		{"a reserved key", ErrReservedKey, func(t *rapid.T, doc map[string]any) {
			key := rapid.SampledFrom([]string{"reserved.k", "Reserved.k", "RESERVED.k", "re" + longS + "erved.k"}).Draw(t, "key")
			pickRule(t, doc)["when"].(map[string]any)["principal"] = map[string]any{"attributes": map[string]any{key: []any{"v"}}}
		}},
		{"external off DENY", ErrExternalEffect, func(t *rapid.T, doc map[string]any) {
			rule := pickRule(t, doc)
			rule["effect"] = rapid.SampledFrom([]string{"ALLOW", "REQUIRE_APPROVAL"}).Draw(t, "effect")
			delete(rule, "reason")
			delete(rule, "obligations")
			rule["when"].(map[string]any)["external"] = map[string]any{"denies": true}
		}},
		{"external false", ErrExternal, func(t *rapid.T, doc map[string]any) {
			rule := pickRule(t, doc)
			rule["effect"] = "DENY"
			delete(rule, "reason")
			delete(rule, "obligations")
			rule["when"].(map[string]any)["external"] = map[string]any{"denies": false}
		}},
		{"a second spelling of a host", ErrIdentifier, func(t *rapid.T, doc map[string]any) {
			host := rapid.SampledFrom([]string{"PASTE.EXAMPLE.NET", "paste.example.net.", "paste.example.net:443", "1.2.3.4:443", "dead:beef"}).Draw(t, "host")
			pickRule(t, doc)["when"].(map[string]any)["destination"] = map[string]any{"host": []any{host}}
		}},
	}
}

func pickRule(t *rapid.T, doc map[string]any) map[string]any {
	rules := doc["rules"].([]any)
	return rules[rapid.IntRange(0, len(rules)-1).Draw(t, "rule")].(map[string]any)
}

// schemaObjects is every object whose keys the format fixes: the document,
// the bundle, each rule, its when, each group and each obligation.
func schemaObjects(doc map[string]any) []map[string]any {
	out := []map[string]any{doc, doc["bundle"].(map[string]any)}
	for _, r := range doc["rules"].([]any) {
		rule := r.(map[string]any)
		when := rule["when"].(map[string]any)
		out = append(out, rule, when)
		for _, g := range sortedNames(when) {
			out = append(out, when[g].(map[string]any))
		}
		obligations, _ := rule["obligations"].([]any)
		for _, o := range obligations {
			out = append(out, o.(map[string]any))
		}
	}
	return out
}

type listSlot struct {
	names bool // a list of enum names rather than of identifiers
	get   func() []any
	set   func([]any)
}

// listSlots is every constraint list: each list a group holds, and each list
// a map constraint holds.
func listSlots(doc map[string]any) []listSlot {
	var out []listSlot
	for _, r := range doc["rules"].([]any) {
		when := r.(map[string]any)["when"].(map[string]any)
		for _, g := range sortedNames(when) {
			group := when[g].(map[string]any)
			for _, k := range sortedNames(group) {
				switch v := group[k].(type) {
				case []any:
					out = append(out, listSlot{k == "effect" || k == "trustZone",
						func() []any { return group[k].([]any) }, func(l []any) { group[k] = l }})
				case map[string]any:
					for _, mk := range sortedNames(v) {
						out = append(out, listSlot{false, func() []any { return v[mk].([]any) }, func(l []any) { v[mk] = l }})
					}
				}
			}
		}
	}
	return out
}

func pickList(t *rapid.T, doc map[string]any) listSlot {
	slots := listSlots(doc)
	if len(slots) == 0 {
		// Rules that name only floors hold no list; give one of them one.
		pickRule(t, doc)["when"].(map[string]any)["action"] = map[string]any{"name": []any{"x"}}
		slots = listSlots(doc)
	}
	return slots[rapid.IntRange(0, len(slots)-1).Draw(t, "list")]
}

type stringSlot struct {
	get func() string
	set func(string)
}

func memberSlot(m map[string]any, key string) stringSlot {
	return stringSlot{func() string { return m[key].(string) }, func(v string) { m[key] = v }}
}

// pickIdentifier picks a string held to the identifier rule: the bundle's id
// or version, a rule's id, or a value of a list of identifiers.
func pickIdentifier(t *rapid.T, doc map[string]any) stringSlot {
	bundle := doc["bundle"].(map[string]any)
	slots := []stringSlot{memberSlot(bundle, "id"), memberSlot(bundle, "version")}
	for _, r := range doc["rules"].([]any) {
		slots = append(slots, memberSlot(r.(map[string]any), "id"))
	}
	for _, list := range listSlots(doc) {
		if list.names {
			continue
		}
		for i := range list.get() {
			slots = append(slots, stringSlot{
				func() string { return list.get()[i].(string) },
				func(v string) { list.get()[i] = v },
			})
		}
	}
	return slots[rapid.IntRange(0, len(slots)-1).Draw(t, "identifier")]
}

// emptyGroup reports a constraint group that is present and holds nothing.
func emptyGroup(w When) bool {
	v := reflect.ValueOf(w)
	for i := range v.NumField() {
		if g := v.Field(i); !g.IsNil() && g.Elem().IsZero() {
			return true
		}
	}
	return false
}

// treeOf reads a model back into the tree a document of it holds, in the
// spellings of section 4, for comparison with the tree that was drawn.
func treeOf(d *Document) map[string]any {
	rules := make([]any, len(d.Rules))
	for i, r := range d.Rules {
		rule := map[string]any{"id": r.ID, "effect": effectNames[r.Effect], "when": whenTree(r.When)}
		if r.Reason != "" {
			rule["reason"] = r.Reason
		}
		if r.Obligations != nil {
			obligations := make([]any, len(r.Obligations))
			for j, o := range r.Obligations {
				tree := map[string]any{"type": o.Type}
				if o.Params != nil {
					params := map[string]any{}
					for k, v := range o.Params {
						params[k] = v
					}
					tree["params"] = params
				}
				if o.Advisory {
					tree["advisory"] = true
				}
				obligations[j] = tree
			}
			rule["obligations"] = obligations
		}
		rules[i] = rule
	}
	return map[string]any{
		"apiVersion": d.APIVersion,
		"bundle":     map[string]any{"id": d.Bundle.ID, "version": d.Bundle.Version, "serial": d.Bundle.Serial, "maxStaleSeconds": d.Bundle.MaxStaleSeconds},
		"rules":      rules,
	}
}

func whenTree(w When) map[string]any {
	out := map[string]any{}
	if p := w.Principal; p != nil {
		out["principal"] = present("id", p.ID, "type", p.Type, "authnStrength", p.AuthnStrength, "tenantId", p.TenantID, "attributes", p.Attributes)
	}
	if a := w.Agent; a != nil {
		out["agent"] = present("id", a.ID, "framework", a.Framework)
	}
	if a := w.Action; a != nil {
		out["action"] = present("name", a.Name, "provider", a.Provider, "protocol", a.Protocol, "kind", a.Kind, "effect", a.Effect)
	}
	if r := w.Resource; r != nil {
		out["resource"] = present("type", r.Type, "id", r.ID, "tenantId", r.TenantID, "environment", r.Environment, "labels", r.Labels)
	}
	if d := w.Destination; d != nil {
		out["destination"] = present("trustZone", d.TrustZone, "host", d.Host)
	}
	if d := w.Data; d != nil {
		out["data"] = map[string]any{"sensitivityAtLeast": nameOf(floorByName, d.SensitivityAtLeast)}
	}
	if f := w.Flow; f != nil {
		out["flow"] = map[string]any{"toxicAtLeast": nameOf(floorByName, f.ToxicAtLeast)}
	}
	if d := w.Delegation; d != nil {
		out["delegation"] = present("scopes", d.Scopes)
	}
	if e := w.External; e != nil {
		out["external"] = map[string]any{"denies": e.Denies}
	}
	return out
}

// present converts each constraint of a group that is set into tree form.
func present(pairs ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i].(string)
		switch v := pairs[i+1].(type) {
		case []string:
			if v != nil {
				out[key] = anys(v)
			}
		case map[string][]string:
			if v != nil {
				m := map[string]any{}
				for k, values := range v {
					m[k] = anys(values)
				}
				out[key] = m
			}
		case []controlv1.EffectClass:
			if v != nil {
				out[key] = namesOf(classByName, v)
			}
		case []controlv1.TrustZone:
			if v != nil {
				out[key] = namesOf(zoneByName, v)
			}
		}
	}
	return out
}

func namesOf[V comparable](table map[string]V, values []V) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = nameOf(table, v)
	}
	return out
}

func nameOf[V comparable](table map[string]V, v V) string {
	for name, value := range table {
		if value == v {
			return name
		}
	}
	return fmt.Sprintf("<no name for %v>", v)
}
