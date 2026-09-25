package rules

import (
	"fmt"
	"math"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

func readDocument(tree any) (*Document, error) {
	var a at
	m, err := closedObject(tree, a, "apiVersion", "bundle", "rules")
	if err != nil {
		return nil, err
	}
	var r reader
	doc := &Document{
		APIVersion: need(&r, m, "apiVersion", a, readAPIVersion),
		Bundle:     need(&r, m, "bundle", a, readBundle),
		Rules:      need(&r, m, "rules", a, readRules),
	}
	if r.err != nil {
		return nil, r.err
	}
	return doc, nil
}

// readAPIVersion refuses every version but the one this build reads, so a
// later format fails loudly instead of being read as this one.
func readAPIVersion(v any, a at) (string, error) {
	s, err := text(v, a)
	if err != nil {
		return "", err
	}
	if s != apiVersion {
		return "", a.refuse(fmt.Errorf("%w: this build reads %s", ErrAPIVersion, apiVersion))
	}
	return s, nil
}

// readBundle reads the document's identity and its author's staleness budget.
// The format has no creation time: none is signed, so a key for one is an
// unknown key.
func readBundle(v any, a at) (Bundle, error) {
	m, err := closedObject(v, a, "id", "version", "serial", "maxStaleSeconds")
	if err != nil {
		return Bundle{}, err
	}
	var r reader
	b := Bundle{
		ID:              need(&r, m, "id", a, identifier),
		Version:         need(&r, m, "version", a, identifier),
		Serial:          need(&r, m, "serial", a, serial),
		MaxStaleSeconds: need(&r, m, "maxStaleSeconds", a, staleBudget),
	}
	if r.err != nil {
		return Bundle{}, r.err
	}
	return b, nil
}

func serial(v any, a at) (int64, error) { return count(v, a, math.MaxInt64) }

func staleBudget(v any, a at) (int64, error) { return count(v, a, maxStaleSeconds) }

// readRules reads the rules in document order, keeping the position each id
// first appeared at.
func readRules(v any, a at) ([]Rule, error) {
	items, err := list(v, a, maxRules)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, len(items))
	first := make(map[string]int, len(items))
	for i, item := range items {
		if out[i], err = readRule(item, a.index(i), first); err != nil {
			return nil, err
		}
		first[out[i].ID] = i
	}
	return out, nil
}

// readRule reads one rule. Its id comes first, so every later refusal names
// the rule it is about; a duplicate names the earlier rule by position.
func readRule(v any, a at, first map[string]int) (Rule, error) {
	m, err := object(v, a)
	if err != nil {
		return Rule{}, err
	}
	var r reader
	id := need(&r, m, "id", a, identifier)
	if r.err != nil {
		return Rule{}, r.err
	}
	if earlier, dup := first[id]; dup {
		return Rule{}, a.key("id").refuse(fmt.Errorf("%w: rules[%d] has it too", ErrDuplicateRuleID, earlier))
	}
	a.rule = id
	if err := onlyKeys(m, a, "id", "effect", "reason", "obligations", "when"); err != nil {
		return Rule{}, err
	}
	rule := Rule{ID: id, Effect: need(&r, m, "effect", a, ruleEffect)}
	rule.Reason = optional(&r, m, "reason", a, func(v any, field at) (string, error) {
		return reason(v, field, rule.Effect)
	})
	rule.Obligations = obligations(&r, m, a, rule.Effect)
	rule.When = need(&r, m, "when", a, readWhen)
	if r.err == nil && rule.When.External != nil && rule.Effect != controlv1.Verdict_VERDICT_DENY {
		r.err = a.key("when").key("external").refuse(ErrExternalEffect)
	}
	if r.err != nil {
		return Rule{}, r.err
	}
	return rule, nil
}

// obligations reads a rule's obligations against its effect. Only
// ALLOW_WITH_OBLIGATIONS and REQUIRE_APPROVAL carry theirs into a decision
// (ADR-0012), so on ALLOW they would be dropped without a word, and on DENY
// nothing runs for them to bind. ALLOW_WITH_OBLIGATIONS with none is never
// produced (ADR-0011).
func obligations(r *reader, m map[string]any, a at, effect controlv1.Verdict) []Obligation {
	if r.err != nil {
		return nil
	}
	_, present := m["obligations"]
	carries := effect == controlv1.Verdict_VERDICT_REQUIRE_APPROVAL ||
		effect == controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS
	switch {
	case present && !carries:
		r.err = a.key("obligations").refuse(ErrObligationsOnEffect)
		return nil
	case !present && effect == controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS:
		r.err = a.key("obligations").refuse(fmt.Errorf("%w: ALLOW_WITH_OBLIGATIONS carries at least one", ErrMissingKey))
		return nil
	}
	return optional(r, m, "obligations", a, readObligations)
}

func readObligations(v any, a at) ([]Obligation, error) {
	return each(v, a, maxObligations, readObligation)
}

func readObligation(v any, a at) (Obligation, error) {
	m, err := closedObject(v, a, "type", "params", "advisory")
	if err != nil {
		return Obligation{}, err
	}
	var r reader
	o := Obligation{
		Type:     need(&r, m, "type", a, obligationType),
		Params:   optional(&r, m, "params", a, params),
		Advisory: optional(&r, m, "advisory", a, flag),
	}
	if r.err != nil {
		return Obligation{}, r.err
	}
	return o, nil
}
