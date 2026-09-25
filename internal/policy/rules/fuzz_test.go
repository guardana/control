package rules

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// FuzzParse feeds Parse arbitrary bytes. Beside never panicking, it holds what
// the loader relies on. A refusal carries no model and no bytes, and an *Error
// that renders as one line. An acceptance returns the canonical form of the
// input, which parses again to itself and to the same model, and a model that
// meets every rule of the document format. And Parse calls a document not
// strict JSON exactly when the canonical form refuses it, so the model never
// reads something other than what the canonical form read.
func FuzzParse(f *testing.F) {
	f.Add(sampleDocument(f, "example.json"))
	f.Add(sampleDocument(f, "every-field.json"))
	for _, doc := range []string{
		docWithRules(denyRuleJSON), exampleCanonical, everyFieldCanonical,
		docWithWhen(`{"principal":{"attributes":{"Team":["a"],"team":["b"]}}}`),
		docWithBundle(`{"id":"p","version":"1","serial":7.0,"maxStaleSeconds":1}`),
		docWithRules(`{"id":"r","effect":"ALLOW","obligations":[{"type":"read_only"}],"when":{"action":{"name":["x"]}}}`),
		docWithWhen(`{"data":{"sensitivityAtLeast":"UNSPECIFIED"}}`),
		docWithWhen(`{"action":{"name":["refund"]},"external":{"denies":true}}`),
		docWithWhen(`{"external":{"denies":false}}`),
		docWithRules(`{"id":"r","effect":"ALLOW","when":{"external":{"denies":true}}}`),
		"", "{}", "[]", "null", `{"apiVersion":"agent-policy/v1alpha1"}`,
	} {
		f.Add([]byte(doc))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		model, canonical, err := Parse(raw)
		checkStrictness(t, raw, err)
		if err != nil {
			checkRefusal(t, raw, model, canonical, err)
			return
		}
		checkAcceptance(t, raw, model, canonical)
	})
}

// checkStrictness: below the size bound, Parse refuses as not strict JSON
// exactly what the canonical form refuses.
func checkStrictness(t *testing.T, raw []byte, err error) {
	t.Helper()
	_, canonErr := canon.CanonicalizeJSON(raw)
	if len(raw) <= 1<<20 && errors.Is(err, ErrNotStrictJSON) != (canonErr != nil) {
		t.Fatalf("Parse(%q) = %v, the canonical form says %v", raw, err, canonErr)
	}
}

func checkRefusal(t *testing.T, raw []byte, model *Document, canonical []byte, err error) {
	t.Helper()
	var e *Error
	if model != nil || canonical != nil || !errors.As(err, &e) {
		t.Fatalf("Parse(%q) refused with %v (%T) beside model %v and %d bytes", raw, err, err, model, len(canonical))
	}
	if msg := err.Error(); msg == "" || strings.ContainsAny(msg, "\n\r") {
		t.Fatalf("refusal renders as %q", msg)
	}
}

func checkAcceptance(t *testing.T, raw []byte, model *Document, canonical []byte) {
	t.Helper()
	want, err := canon.CanonicalizeJSON(raw)
	if err != nil || !bytes.Equal(canonical, want) {
		t.Fatalf("Parse(%q) returned %q, the canonical form is %q (%v)", raw, canonical, want, err)
	}
	again, canonicalAgain, err := Parse(canonical)
	if err != nil || !bytes.Equal(canonicalAgain, canonical) || !reflect.DeepEqual(again, model) {
		t.Fatalf("the canonical form %q does not parse to itself and the same model: %v", canonical, err)
	}
	if problem := modelProblem(model); problem != "" {
		t.Fatalf("Parse(%q) accepted a model that %s", raw, problem)
	}
}

// modelProblem checks an accepted model against section 4, from the tables
// this package's tests write out rather than from the parser's own.
func modelProblem(d *Document) string {
	if d.APIVersion != "agent-policy/v1alpha1" || d.Bundle.Serial <= 0 || d.Bundle.MaxStaleSeconds <= 0 {
		return "has a wrong version, serial or budget"
	}
	if len(d.Rules) == 0 || len(d.Rules) > 4096 {
		return "has no rule or too many"
	}
	seen := map[string]bool{}
	for _, r := range d.Rules {
		if seen[r.ID] || r.ID == "" {
			return "repeats or omits a rule id"
		}
		seen[r.ID] = true
		if problem := ruleProblem(r); problem != "" {
			return problem
		}
	}
	if path := emptyContainer(reflect.ValueOf(d), "document"); path != "" {
		return "holds an empty list or map at " + path
	}
	return ""
}

func ruleProblem(r Rule) string {
	authorable, known := authorableWant[r.Effect]
	switch {
	case !known:
		return "has a rule effect outside the four"
	case r.Reason != "" && !slices.Contains(authorable[1:], r.Reason):
		return "has a reason the effect may not name, or a default spelled out"
	case reflect.DeepEqual(r.When, When{}):
		return "has a rule that constrains nothing"
	case emptyGroup(r.When):
		return "has a group that constrains nothing"
	case r.When.External != nil && (r.Effect != controlv1.Verdict_VERDICT_DENY || !r.When.External.Denies):
		return "reads the external answer other than as a denial on a DENY rule"
	}
	return obligationsProblem(r)
}

func obligationsProblem(r Rule) string {
	carries := r.Effect == controlv1.Verdict_VERDICT_REQUIRE_APPROVAL || r.Effect == controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS
	switch {
	case len(r.Obligations) > 8:
		return "has too many obligations"
	case !carries && r.Obligations != nil:
		return "has obligations on ALLOW or DENY"
	case r.Effect == controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS && len(r.Obligations) == 0:
		return "has ALLOW_WITH_OBLIGATIONS with no obligation"
	}
	for _, o := range r.Obligations {
		if !slices.Contains(catalogueWant, o.Type) {
			return "has an obligation type outside the catalogue"
		}
	}
	return ""
}

// emptyContainer returns the path of the first list or map in v that is
// present and empty, or "". Parse leaves an absent one nil and never an empty
// one, which would read as either nothing or everything.
func emptyContainer(v reflect.Value, path string) string {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			return emptyContainer(v.Elem(), path)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if p := emptyContainer(v.Field(i), path+"."+v.Type().Field(i).Name); p != "" {
				return p
			}
		}
	case reflect.Slice, reflect.Map:
		return emptyCollection(v, path)
	}
	return ""
}

func emptyCollection(v reflect.Value, path string) string {
	if v.IsNil() {
		return ""
	}
	if v.Len() == 0 {
		return path
	}
	if v.Kind() == reflect.Slice {
		for i := range v.Len() {
			if p := emptyContainer(v.Index(i), path+"[]"); p != "" {
				return p
			}
		}
		return ""
	}
	for iter := v.MapRange(); iter.Next(); {
		if p := emptyContainer(iter.Value(), path+"{}"); p != "" {
			return p
		}
	}
	return ""
}
