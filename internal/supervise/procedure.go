package supervise

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// ProcedureSchemaVersion is the one procedure version this package reads.
const ProcedureSchemaVersion = "0.1"

// MaxProcedureBytes bounds a procedure document.
const MaxProcedureBytes = 65536

// The rules a procedure configures, in the order a report lists them.
const (
	RuleRepeatedDenial        = "REPEATED_DENIAL"
	RuleStepOutsideProcedure  = "STEP_OUTSIDE_PROCEDURE"
	RuleDeadlineExceeded      = "DEADLINE_EXCEEDED"
	RuleRequiredStepSkipped   = "REQUIRED_STEP_SKIPPED"
	RuleStepOutOfOrder        = "STEP_OUT_OF_ORDER"
	RuleContinuedAfterFailure = "CONTINUED_AFTER_FAILURE"
)

// RuleVersion is the version of every rule above; a change to what a rule
// fires on is a new version, and so a new finding id.
const RuleVersion = "1"

var ruleIDs = [...]string{
	RuleRepeatedDenial, RuleStepOutsideProcedure, RuleDeadlineExceeded,
	RuleRequiredStepSkipped, RuleStepOutOfOrder, RuleContinuedAfterFailure,
}

// Step is one step of a procedure: the tool and upstream a plane sees it as,
// and the names a source reports it under.
type Step struct {
	ID         string
	Tool       string
	Upstream   string
	ObservedAs []string
	Required   bool
}

// Allowed is a tool the run may call besides its steps.
type Allowed struct {
	Tool       string
	Upstream   string
	ObservedAs []string
}

// RuleSpec is what a finding of one rule carries.
type RuleSpec struct {
	Severity   controlv1.FindingSeverity
	Escalation findingv1alpha1.Escalation
}

// Procedure is a procedure document as ReadProcedure accepted it. Only
// ReadProcedure makes one Evaluate takes.
type Procedure struct {
	id, version string
	steps       []Step
	// after maps a step's id to the ids of the steps it must follow.
	after           map[string][]string
	allow           []Allowed
	maxDenials      uint32
	deadlineSeconds uint32
	rules           map[string]RuleSpec
	digest          string
}

// ID is the procedure's id.
func (p *Procedure) ID() string { return p.id }

// Version is the procedure's version.
func (p *Procedure) Version() string { return p.version }

// Digest is the lowercase hex SHA-256 of the document's RFC 8785 canonical
// form: member order and white space do not change it, an edit does.
func (p *Procedure) Digest() string { return p.digest }

// Steps are the procedure's steps in document order.
func (p *Procedure) Steps() []Step { return p.steps }

// ReadProcedure reads a procedure document strictly: one JSON object of at
// most MaxProcedureBytes, every member required and none unknown or named
// twice, except max_denials and deadline_seconds, whose absence turns their
// rule off. A cycle in the order, a step it names that the steps lack, and a
// tool, upstream or reported name that two entries share are refused.
func ReadProcedure(b []byte) (*Procedure, error) {
	p, err := readProcedure(b)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProcedure, err)
	}
	return p, nil
}

var (
	procedureMembers = []string{
		"schema_version", "procedure_id", "version", "steps", "order", "allow", "rules",
	}
	procedureOptional = []string{"max_denials", "deadline_seconds"}
)

func readProcedure(b []byte) (*Procedure, error) {
	if len(b) > MaxProcedureBytes {
		return nil, fmt.Errorf("%d bytes, bound %d", len(b), MaxProcedureBytes)
	}
	canonical, err := canon.CanonicalizeJSON(b)
	if err != nil {
		return nil, err
	}
	o, err := object(b, slices.Concat(procedureMembers, procedureOptional), procedureMembers)
	if err != nil {
		return nil, err
	}
	r := &reader{}
	if r.ident(o, "schema_version") != ProcedureSchemaVersion && r.err == nil {
		return nil, errors.New("schema_version is not " + ProcedureSchemaVersion)
	}
	p := &Procedure{id: r.ident(o, "procedure_id"), version: r.ident(o, "version")}
	p.steps = r.steps(o["steps"])
	p.allow = r.allowed(o["allow"])
	p.after = r.order(o["order"])
	p.maxDenials = r.count(o, "max_denials")
	p.deadlineSeconds = r.count(o, "deadline_seconds")
	p.rules = r.rules(o["rules"])
	if r.err != nil {
		return nil, r.err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	p.digest = hex.EncodeToString(sum[:])
	return p, nil
}
