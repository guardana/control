package core

import (
	"errors"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core/delegation"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/pkg/contract"
)

// decision is one run of the kernel's fixed order (ADR-0012): what the steps
// found, in the order they found it.
type decision struct {
	now      time.Time
	operator time.Duration // the operator's staleness budget
	env      *controlv1.ActionEnvelope
	req      Request
	snap     *policy.Snapshot

	codes  []string
	denied bool // a matched DENY or one of the kernel's own
	causes undecided
	digest string
	inputs match.Inputs
	result match.Result
	// needsExternal is whether a rule's value turns on the external answer;
	// consulted, whether there was an answer for it to turn on.
	needsExternal bool
	consulted     bool
}

func newDecision(k *Kernel, req Request, snap *policy.Snapshot) *decision {
	return &decision{
		now:      k.clock(),
		operator: k.maxStale,
		env:      proto.CloneOf(req.Envelope),
		req:      req,
		snap:     snap,
		causes: undecided{
			failOpenRead: k.failOpenRead,
			snapshot:     snap != nil,
		},
	}
}

// add lists a cause once, in step order.
func (d *decision) add(code string) {
	if !slices.Contains(d.codes, code) {
		d.codes = append(d.codes, code)
	}
}

func (d *decision) run(k *Kernel) {
	if !d.admit() {
		return
	}
	d.checkDelegation()
	d.checkTenant()
	if d.snap == nil {
		d.add(codePolicyUnavailable)
		d.causes.availability = true
		return
	}
	d.checkFreshness()
	d.evaluate(k.applicable)
}

// admit is steps 1 and 2: the refusal handed in, or Validate's on the clone;
// then the digest and the arguments hash. Each stops the decision.
func (d *decision) admit() bool {
	refusal := d.req.Refusal
	if refusal == nil {
		refusal = contract.Validate(d.env)
	}
	if refusal != nil {
		return d.refuse(refusalCode(refusal))
	}
	digest, err := canon.DigestV1(d.env, d.req.AuthorizedArgs)
	if err != nil {
		return d.refuse(digestCode(err))
	}
	if sent := d.env.GetArguments().GetCanonicalHash(); sent != "" {
		held, err := canon.ArgumentsHashV1(d.req.AuthorizedArgs)
		if err != nil {
			return d.refuse(digestCode(err))
		}
		if held != sent {
			return d.refuse(codeInvalidFieldValue)
		}
	}
	d.digest = digest
	return true
}

func (d *decision) refuse(code string) bool {
	d.add(code)
	d.causes.input = true
	return false
}

// refusalCode is the contract page's table, by sentinel. A refusal that
// matches none of them could not be read as this contract at all.
func refusalCode(err error) string {
	switch {
	case errors.Is(err, contract.ErrUnsupportedSchema), errors.Is(err, contract.ErrUnknownField), errors.Is(err, contract.ErrInvalidEnum):
		return codeUnsupportedSchema
	case errors.Is(err, contract.ErrMissingField):
		return codeRequiredFieldAbsent
	case errors.Is(err, contract.ErrTooLarge):
		return codeLimitExceeded
	case errors.Is(err, contract.ErrInvalidValue):
		return codeInvalidFieldValue
	}
	return codeMalformedInput
}

// digestCode: the two bounds of the canonical form are LIMIT_EXCEEDED, every
// other refusal of the arguments is a value the contract states a rule for.
func digestCode(err error) string {
	if errors.Is(err, canon.ErrArgumentsTooLarge) || errors.Is(err, canon.ErrTooDeep) {
		return codeLimitExceeded
	}
	return codeInvalidFieldValue
}

// checkDelegation is step 3: a refused chain is a DENY of the kernel's own,
// and the policy then sees no delegation at all. A chain that passes hands
// the policy both halves of the delegation input.
func (d *decision) checkDelegation() {
	effective, err := delegation.Check(d.env.GetDelegation(), d.now)
	if err != nil {
		d.denied = true
		var refused *delegation.Error
		if errors.As(err, &refused) {
			d.add(refused.Code)
		}
		return
	}
	d.inputs.Delegated = effective.Delegated
	d.inputs.Scopes = effective.Scopes
}

// checkTenant is step 4: two tenants named and different is DENY on any
// class; one named on a material class cannot be compared.
func (d *decision) checkTenant() {
	principal, resource := d.env.GetPrincipal().GetTenantId(), d.env.GetResource().GetTenantId()
	switch {
	case principal != "" && resource != "" && principal != resource:
		d.add(codeTenantMismatch)
		d.denied = true
	case (principal == "") != (resource == "") && contract.IsMaterial(d.env.GetAction().GetEffect()):
		d.add(codeTenantUndetermined)
		d.causes.input = true
	}
}

// stale is step 6's condition: no snapshot, or an age over the smaller of
// the author's and the operator's budget, or a negative one.
func (d *decision) stale() bool {
	if d.snap == nil {
		return true
	}
	age := d.now.Sub(d.snap.ConfirmedAt())
	return age < 0 || age > min(d.snap.MaxStale(), d.operator)
}

// checkFreshness is step 6: a stale snapshot is a cause, and evaluation
// continues so that a stale DENY is still a DENY.
func (d *decision) checkFreshness() {
	if d.stale() {
		d.add(codePolicyStale)
		d.causes.availability = true
	}
}

// evaluate is steps 7 and 8. An INDETERMINATE the evaluator reaches is a cause
// in the request: an undetermined rule, or an evaluation that could not
// happen, and the table never opens a read for either. An external answer
// that is missing leaves its veto undetermined, so its silence is such a
// cause too. The answer's own code follows the rules' codes.
func (d *decision) evaluate(applicable map[string]bool) {
	d.inputs.Flow = d.req.Flow
	d.inputs.External = d.req.External.input()
	d.result = d.snap.Evaluate(d.env, d.inputs)
	d.causes.determinate = d.result.Determinate
	for _, code := range d.result.ReasonCodes {
		d.add(code)
	}
	d.needsExternal = len(d.result.ReadExternal) > 0
	if code := d.req.External.code(); d.needsExternal && code != "" {
		d.add(code)
		d.consulted = true
	}
	switch d.result.Verdict {
	case controlv1.Verdict_VERDICT_DENY:
		d.denied = true
	case controlv1.Verdict_VERDICT_REQUIRE_APPROVAL, controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS, controlv1.Verdict_VERDICT_ALLOW:
	default:
		d.causes.input = true
	}
	for _, o := range d.result.Obligations {
		if !o.GetAdvisory() && !applicable[o.GetType()] {
			d.add(codeObligationNotUnderstood)
			d.denied = true
			return
		}
	}
}

// verdict is step 9, ADR-0012's table: a DENY of any origin wins; then any
// cause; then what the evaluator concluded.
func (d *decision) verdict() controlv1.Verdict {
	switch {
	case d.denied:
		return controlv1.Verdict_VERDICT_DENY
	case d.causes.input || d.causes.availability:
		return controlv1.Verdict_VERDICT_INDETERMINATE
	case d.result.Verdict == controlv1.Verdict_VERDICT_REQUIRE_APPROVAL,
		d.result.Verdict == controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS,
		d.result.Verdict == controlv1.Verdict_VERDICT_ALLOW:
		return d.result.Verdict
	}
	return controlv1.Verdict_VERDICT_INDETERMINATE
}

// outcome is the last step, what to enforce, and the decision's fields. The
// second clock reading measures the latency and nothing else.
func (d *decision) outcome(k *Kernel) Outcome {
	verdict := d.verdict()
	d.causes.effect = d.env.GetAction().GetEffect()
	action, opened := actionFor(verdict, d.causes)
	if opened {
		d.add(codeFailOpenRead)
	}
	// The field is computed on every branch; POLICY_STALE is a code only
	// where step 6 ran, so a refused request reports a stale snapshot
	// without it.
	freshness := controlv1.PolicyFreshness_POLICY_FRESHNESS_FRESH
	if d.stale() {
		freshness = controlv1.PolicyFreshness_POLICY_FRESHNESS_STALE
	}
	var loadedAt *timestamppb.Timestamp
	if d.snap != nil {
		loadedAt = timestamppb.New(d.snap.ConfirmedAt())
	}
	latency := max(0, k.clock().Sub(d.now).Microseconds())
	var instance string
	if d.consulted {
		instance = k.decisionPoint
	}
	return Outcome{
		Decision: &controlv1.Decision{
			SchemaVersion:      schemaVersion,
			DecisionId:         k.newID(),
			RequestId:          requestID(d.env),
			ActionDigest:       d.digest,
			PolicyBundleDigest: d.snap.Ref().GetDigest(),
			PolicyRuleIds:      slices.Concat(d.result.RuleIDs, d.result.Indeterminate),
			Verdict:            verdict,
			ReasonCodes:        d.codes,
			Obligations:        d.obligations(verdict),
			DecisionLatencyUs:  latency,
			PdpType:            pdpType,
			PdpInstance:        instance,
			EnforcementMode:    controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE,
			PolicyFreshness:    freshness,
			PolicyLoadedAt:     loadedAt,
			DecidedAt:          timestamppb.New(d.now),
		},
		Action:        action,
		NeedsExternal: d.needsExternal,
	}
}

// obligations are conditions on a call that proceeds, so they travel only on
// the two verdicts that let it proceed under them. A DENY from step 8 names
// the one it could not apply by its code, and a stale bundle's INDETERMINATE
// by POLICY_STALE; neither carries a condition.
func (d *decision) obligations(verdict controlv1.Verdict) []*controlv1.Obligation {
	if verdict != controlv1.Verdict_VERDICT_REQUIRE_APPROVAL && verdict != controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS {
		return nil
	}
	obligations := make([]*controlv1.Obligation, 0, len(d.result.Obligations))
	for _, o := range d.result.Obligations {
		obligations = append(obligations, proto.CloneOf(o))
	}
	return obligations
}

// requestID names the request when the envelope's id passes the identifier
// rule and its bound, so a refusal about the identifier never copies it.
func requestID(env *controlv1.ActionEnvelope) string {
	id := env.GetRequestId()
	if len(id) > contract.MaxStringBytes || contract.CheckIdentifier(id) != nil {
		return ""
	}
	return id
}
