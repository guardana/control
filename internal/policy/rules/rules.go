// Package rules reads a policy document, agent-policy/v1alpha1, strictly, and
// holds its model. What the model means is ADR-0012's.
package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// Document is a parsed and validated policy. It is read-only after Parse.
type Document struct {
	APIVersion string
	Bundle     Bundle
	Rules      []Rule
}

// Bundle is the document's own identity and its author's staleness budget.
type Bundle struct {
	ID              string
	Version         string
	Serial          int64
	MaxStaleSeconds int64
}

// Rule is one rule. Effect is ALLOW, DENY, REQUIRE_APPROVAL or
// ALLOW_WITH_OBLIGATIONS; Reason is empty for the effect's own default.
type Rule struct {
	ID          string
	Effect      controlv1.Verdict
	Reason      string
	Obligations []Obligation
	When        When
}

// Obligation is a condition a rule attaches to a call it lets proceed.
type Obligation struct {
	Type     string
	Params   map[string]string
	Advisory bool
}

// When holds a rule's constraint groups. A nil group or a nil list constrains
// nothing; Parse never leaves an empty list, which would read as either nothing
// or everything.
type When struct {
	Principal   *PrincipalWhen
	Agent       *AgentWhen
	Action      *ActionWhen
	Resource    *ResourceWhen
	Destination *DestinationWhen
	Data        *DataWhen
	Flow        *FlowWhen
	Delegation  *DelegationWhen
	External    *ExternalWhen
}

// PrincipalWhen constrains the principal.
type PrincipalWhen struct {
	ID            []string
	Type          []string
	AuthnStrength []string
	TenantID      []string
	Attributes    map[string][]string
}

// AgentWhen constrains the agent.
type AgentWhen struct {
	ID        []string
	Framework []string
}

// ActionWhen constrains the action.
type ActionWhen struct {
	Name     []string
	Provider []string
	Protocol []string
	Kind     []string
	Effect   []controlv1.EffectClass
}

// ResourceWhen constrains the resource.
type ResourceWhen struct {
	Type        []string
	ID          []string
	TenantID    []string
	Environment []string
	Labels      map[string][]string
}

// DestinationWhen constrains where the call sends data.
type DestinationWhen struct {
	TrustZone []controlv1.TrustZone
	Host      []string
}

// DataWhen constrains the sensitivity the envelope's own labels declare.
type DataWhen struct {
	SensitivityAtLeast controlv1.Sensitivity
}

// FlowWhen constrains the flow of the run the call belongs to.
type FlowWhen struct {
	ToxicAtLeast controlv1.Sensitivity
}

// DelegationWhen constrains the scopes a delegated call holds.
type DelegationWhen struct {
	Scopes []string
}

// ExternalWhen constrains the call by the external decision point's answer.
// Denies is its one form: the constraint holds when the decision point denies
// the call. Parse accepts it on a DENY rule only, so the answer can take a
// grant away and never give one.
type ExternalWhen struct {
	Denies bool
}

const (
	apiVersion = "agent-policy/v1alpha1"

	// The bounds of the format. Each caps work a document can make the loader
	// do before anything has been decided with it.
	maxDocumentBytes = 1 << 20
	maxRules         = 4096
	maxValues        = 64
	maxObligations   = 8

	// maxStaleSeconds is the largest budget a time.Duration holds in whole
	// seconds. A larger one would wrap when the snapshot converts it, and the
	// budget enforced would not be the one signed.
	maxStaleSeconds = math.MaxInt64 / int64(time.Second)
)

// Parse reads raw as an agent-policy/v1alpha1 document and returns its model and
// the canonical bytes of raw, or a refusal.
//
// The canonical form reads raw first and refuses what it refuses in any
// document: a duplicate key, keys that fold together, a float, an integer
// outside the JSON-safe range, invalid UTF-8. The model is then read from the
// canonical bytes, so it holds exactly what the signed bytes say. Every
// refusal is an *Error, and the one returned is the first in a fixed order:
// the document, its bundle, then each rule in document order, each field in
// the order of the format.
func Parse(raw []byte) (*Document, []byte, error) {
	if len(raw) > maxDocumentBytes {
		return nil, nil, at{}.refuse(fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(raw), maxDocumentBytes))
	}
	canonical, err := canon.CanonicalizeJSON(raw)
	if err != nil {
		return nil, nil, at{}.refuse(notStrict(err))
	}
	tree, err := decode(canonical)
	if err != nil {
		return nil, nil, at{}.refuse(notStrict(err))
	}
	doc, err := readDocument(tree)
	if err != nil {
		return nil, nil, err
	}
	return doc, canonical, nil
}

// notStrict classifies a refusal of the canonical form or of the decoder
// without its text. That text names the refused value by an RFC 6901 pointer,
// which spells out every map key on the way, and a key is the author's
// content; a syntax refusal quotes a byte of the input. What is kept is the
// canonical form's sentinel, for errors.Is, and a syntax error's offset.
func notStrict(err error) error {
	var syntax *json.SyntaxError
	switch {
	case errors.Is(err, canon.ErrTooDeep):
		return fmt.Errorf("%w: %w", ErrNotStrictJSON, canon.ErrTooDeep)
	case errors.Is(err, canon.ErrUnsupportedValue):
		return fmt.Errorf("%w: %w", ErrNotStrictJSON, canon.ErrUnsupportedValue)
	case errors.As(err, &syntax):
		return fmt.Errorf("%w: a syntax error at byte %d", ErrNotStrictJSON, syntax.Offset)
	}
	return fmt.Errorf("%w: not well-formed JSON", ErrNotStrictJSON)
}

// decode reads the canonical form's own output into maps and slices. Nothing
// here matches a key to a field: the readers compare every key with ==, so a
// key that differs from a name of the format in case, or in a character that
// folds to it, is an unknown key rather than that name. The canonical form has
// already refused what a decode would hide, a repeated key first of all, so
// this cannot fail on its output; were it to, the document is refused.
func decode(canonical []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return tree, nil
}
