package match

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/rules"
)

// Compile's refusals. Parse refuses each of these as well. Compile refuses them
// again because the kernel reads the program, not the document, and a program
// built from a document Parse never produced must not read a rule differently
// from the way its author wrote it.
var (
	errNoDocument   = errors.New("no document")
	errRuleID       = errors.New("a rule id that is empty or repeats an earlier one")
	errEffect       = errors.New("not one of the four rule effects")
	errReason       = errors.New("a reason this effect may not carry")
	errObligations  = errors.New("obligations on an effect that carries none, or none on ALLOW_WITH_OBLIGATIONS")
	errNoConstraint = errors.New("a rule that constrains nothing")
	errEmptyList    = errors.New("an empty list, which reads as either nothing or everything")
	errUnusable     = errors.New("a value no envelope can match: empty, unspecified or undeclared")
	errFloor        = errors.New("a floor this build cannot compare against")
	// errExternal and errExternalEffect keep the decision point to a veto: a
	// denial it answers, read by a DENY rule.
	errExternal       = errors.New("an external constraint in a form other than a denial")
	errExternalEffect = errors.New("an external constraint on an effect other than DENY")
)

// compileError names the rule by its position and the field in the document's
// own spelling. It never repeats a value from the document.
type compileError struct {
	rule  int // -1 for the document as a whole
	field string
	err   error
}

func (e *compileError) Error() string {
	if e.rule < 0 {
		return "match: " + e.err.Error()
	}
	return "match: rules[" + strconv.Itoa(e.rule) + "]." + e.field + ": " + e.err.Error()
}

func (e *compileError) Unwrap() error { return e.err }

// rule is one compiled rule. Nothing in it aliases the document.
type rule struct {
	id          string
	effect      controlv1.Verdict
	reason      string
	obligations []obligation
	constraints []constraint
	// external is whether the rule also reads the external answer, which
	// holds is the only one to read, so that it can tell whether the rule's
	// value turns on it.
	external bool
}

// obligation is one obligation as the program keeps it, with its identity in
// the union computed once.
type obligation struct {
	typ      string
	params   map[string]string // never handed out; fresh copies it
	advisory bool
	key      string
}

func compileRule(i int, r *rules.Rule) (rule, error) {
	refuse := func(field string, err error) (rule, error) {
		return rule{}, &compileError{rule: i, field: field, err: err}
	}
	if r.ID == "" {
		return refuse("id", errRuleID)
	}
	if defaultReason(r.Effect) == "" {
		return refuse("effect", errEffect)
	}
	reason, ok := reasonFor(r.Effect, r.Reason)
	if !ok {
		return refuse("reason", errReason)
	}
	obligations, err := compileObligations(r)
	if err != nil {
		return refuse("obligations", err)
	}
	w := compileWhen(&r.When)
	switch {
	case w.err != nil:
		return refuse(w.field, w.err)
	case w.external && r.Effect != controlv1.Verdict_VERDICT_DENY:
		return refuse("when.external", errExternalEffect)
	}
	return rule{id: r.ID, effect: r.Effect, reason: reason, obligations: obligations, constraints: w.constraints, external: w.external}, nil
}

// compileObligations holds each effect to what it may carry. The union takes
// every matched rule's obligations, which is ADR-0012's union over
// ALLOW_WITH_OBLIGATIONS and REQUIRE_APPROVAL only while ALLOW and DENY rules
// carry none; and an ALLOW_WITH_OBLIGATIONS rule with none would produce the
// verdict ADR-0011 says is never produced.
func compileObligations(r *rules.Rule) ([]obligation, error) {
	switch r.Effect {
	case controlv1.Verdict_VERDICT_ALLOW, controlv1.Verdict_VERDICT_DENY:
		if len(r.Obligations) > 0 {
			return nil, errObligations
		}
	case controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS:
		if len(r.Obligations) == 0 {
			return nil, errObligations
		}
	}
	out := make([]obligation, len(r.Obligations))
	for i, o := range r.Obligations {
		out[i] = obligation{typ: o.Type, params: maps.Clone(o.Params), advisory: o.Advisory, key: obligationKey(o)}
	}
	return out, nil
}

// obligationKey is an obligation's identity in the union: its type, its
// parameters and its advisory flag. Every string is length-prefixed, so no
// choice of strings gives two different obligations one key.
func obligationKey(o rules.Obligation) string {
	var b strings.Builder
	field := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
	}
	field(o.Type)
	field(strconv.FormatBool(o.Advisory))
	for _, k := range slices.Sorted(maps.Keys(o.Params)) {
		field(k)
		field(o.Params[k])
	}
	return b.String()
}

// fresh is the obligation as Evaluate hands it out, sharing nothing with the
// program.
func (o *obligation) fresh() *controlv1.Obligation {
	return &controlv1.Obligation{Type: o.typ, Params: maps.Clone(o.params), Advisory: o.advisory}
}
