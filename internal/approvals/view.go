package approvals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// View is the projection beside a record: what an approver has to see to
// answer, and nothing else. The plane writes it at hold time and never reads
// it back as truth, so a projection that is missing, stale or corrupt costs a
// listing its readable fields and costs an approval nothing.
//
// It carries no free text. arguments.redacted_preview and delegation[].reason
// are the envelope's two free-text fields, and a second human-readable copy of
// a held request does not widen what ADR-0004 and invariant 9 allow to be
// stored. The fields are filled from the envelope and the decision by ViewOf
// alone, so nothing a caller composes can arrive here instead.
//
// The readable fields are not bound to the digests beside them: the binding is
// over the authorized argument bytes, which no record holds. Anything that
// prints a View says so.
type View struct {
	SchemaVersion      string    `json:"schema_version"`
	ApprovalID         string    `json:"approval_id"`
	RequestID          string    `json:"request_id"`
	ActionDigest       string    `json:"action_digest"`
	PolicyBundleDigest string    `json:"policy_bundle_digest"`
	Principal          string    `json:"principal"`
	Agent              string    `json:"agent"`
	Action             string    `json:"action"`
	Provider           string    `json:"provider"`
	ResourceType       string    `json:"resource_type"`
	ResourceID         string    `json:"resource_id"`
	EffectClass        string    `json:"effect_class"`
	RuleIDs            []string  `json:"rule_ids"`
	RequestedAt        time.Time `json:"requested_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// ViewOf renders the projection of one held request. It reads named fields off
// the envelope and the decision and copies nothing else, which is how the two
// free-text fields stay out of it.
func ViewOf(a *controlv1.Approval, env *controlv1.ActionEnvelope, decision *controlv1.Decision) View {
	return View{
		SchemaVersion:      SchemaVersion,
		ApprovalID:         a.GetApprovalId(),
		RequestID:          a.GetRequestId(),
		ActionDigest:       a.GetActionDigest(),
		PolicyBundleDigest: a.GetPolicyBundleDigest(),
		Principal:          env.GetPrincipal().GetId(),
		Agent:              env.GetAgent().GetId(),
		Action:             env.GetAction().GetName(),
		Provider:           env.GetAction().GetProvider(),
		ResourceType:       env.GetResource().GetType(),
		ResourceID:         env.GetResource().GetId(),
		EffectClass:        env.GetAction().GetEffect().String(),
		RuleIDs:            append([]string(nil), decision.GetPolicyRuleIds()...),
		RequestedAt:        a.GetRequestedAt().AsTime(),
		ExpiresAt:          a.GetExpiresAt().AsTime(),
	}
}

// encodeView renders the projection as one JSON object and a newline.
func encodeView(v View) ([]byte, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return append(out, '\n'), nil
}

// decodeView reads a projection. A field this build cannot name refuses it:
// the projection is not truth, but a listing that quietly dropped half of one
// would be read as the whole of it.
func decodeView(data []byte) (View, error) {
	var v View
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return View{}, fmt.Errorf("%w: the projection: %s", ErrMalformed, cause(err.Error()))
	}
	return v, nil
}
