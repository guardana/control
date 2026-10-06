package reaction

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy"
)

const (
	memberPayloadType  = "payloadType"
	memberPayload      = "payload"
	memberSignatures   = "signatures"
	memberSigKeyID     = "keyid"
	memberSigSignature = "sig"
)

// ErrLineMarshal is a line its writer would not read back as itself.
const ErrLineMarshal Error = "reaction: the line would not read back as itself"

// Marshal is the header's line, its newline left out, refused unless
// ParseLine reads it back as h.
func (h Header) Marshal() ([]byte, error) {
	return marshalLine(Line{Kind: KindHeader, Header: h}, map[string]any{
		memberKind: kindHeader, memberVersion: ListVersion, memberListID: h.ListID, memberRouteID: h.RouteID,
		memberRouteSerial: h.RouteSerial, memberRouteDigest: h.RouteDigest,
	})
}

// Marshal is the stop's line, its newline left out, refused unless ParseLine
// reads it back as s: an entry id that is not EntryID's of the finding id,
// or a time that is not a whole second, among the rest.
func (s Stop) Marshal() ([]byte, error) {
	return marshalLine(Line{Kind: KindStop, Stop: s}, map[string]any{
		memberKind: kindStop, memberVersion: ListVersion, memberEntryID: s.EntryID, memberTenantID: s.TenantID,
		memberRunID: s.RunID, memberFindingID: s.FindingID, memberProcedure: s.ProcedureID,
		memberProcVersion: s.ProcedureVersion, memberProcDigest: s.ProcedureDigest, memberRuleID: s.RuleID,
		memberRuleVer: s.RuleVersion, memberCreatedAt: policy.FormatIssuedAt(s.CreatedAt),
		memberExpiresAt: policy.FormatIssuedAt(s.ExpiresAt),
	})
}

// Marshal is the covered line, its newline left out, refused unless
// ParseLine reads it back as c.
func (c Covered) Marshal() ([]byte, error) {
	return marshalLine(Line{Kind: KindCovered, Covered: c}, map[string]any{
		memberKind: kindCovered, memberVersion: ListVersion, memberFindingID: c.FindingID,
		memberTenantID: c.TenantID, memberRunID: c.RunID, memberCreatedAt: policy.FormatIssuedAt(c.CreatedAt),
	})
}

// Marshal is the lift line, its newline left out, refused unless ParseLine
// reads it back as l. It checks no signature: Judge does.
func (l LiftLine) Marshal() ([]byte, error) {
	sigs := make([]any, 0, len(l.Envelope.Signatures))
	for _, s := range l.Envelope.Signatures {
		sigs = append(sigs, map[string]any{
			memberSigKeyID: s.KeyID, memberSigSignature: base64.StdEncoding.EncodeToString(s.Signature),
		})
	}
	return marshalLine(Line{Kind: KindLift, Lift: l}, map[string]any{
		memberKind: kindLift, memberVersion: ListVersion, memberRunID: l.RunID, memberThrough: l.ThroughLine,
		memberEnvelope: map[string]any{
			memberPayloadType: l.Envelope.PayloadType,
			memberPayload:     base64.StdEncoding.EncodeToString(l.Envelope.Payload),
			memberSignatures:  sigs,
		},
	})
}

func marshalLine(want Line, members map[string]any) ([]byte, error) {
	raw, err := canon.Canonicalize(members)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLineMarshal, canon.ErrUnsupportedValue)
	}
	got, err := ParseLine(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLineMarshal, err)
	}
	if !sameLine(got, want) {
		return nil, ErrLineMarshal
	}
	return raw, nil
}

func sameLine(a, b Line) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindHeader:
		return a.Header == b.Header
	case KindStop:
		return sameStop(a.Stop, b.Stop)
	case KindCovered:
		x, y := a.Covered, b.Covered
		return x.FindingID == y.FindingID && x.TenantID == y.TenantID && x.RunID == y.RunID && x.CreatedAt.Equal(y.CreatedAt)
	case KindLift:
		return a.Lift.RunID == b.Lift.RunID && a.Lift.ThroughLine == b.Lift.ThroughLine && sameEnvelope(a.Lift.Envelope, b.Lift.Envelope)
	}
	return false
}

func sameStop(a, b Stop) bool {
	times := a.CreatedAt.Equal(b.CreatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
	a.CreatedAt, a.ExpiresAt, b.CreatedAt, b.ExpiresAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return times && a == b
}

func sameEnvelope(a, b Envelope) bool {
	if a.PayloadType != b.PayloadType || !bytes.Equal(a.Payload, b.Payload) || len(a.Signatures) != len(b.Signatures) {
		return false
	}
	for i := range a.Signatures {
		if a.Signatures[i].KeyID != b.Signatures[i].KeyID || !bytes.Equal(a.Signatures[i].Signature, b.Signatures[i].Signature) {
			return false
		}
	}
	return true
}
