package reaction

import (
	"bytes"
	"fmt"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/strictjson"
)

const (
	// LiftKind is the one kind a lift may name.
	LiftKind = "reaction-lift/v1alpha1"
	// LiftVersion is the one version a lift may name, in this one spelling,
	// so one lift has one payload and one signature.
	LiftVersion = "1.0"
	// MaxLiftBytes bounds a lift's payload.
	MaxLiftBytes = 4096
	// MaxListIDBytes bounds the list id a lift names.
	MaxListIDBytes = 128
	// MaxRunIDBytes bounds the run id a lift names.
	MaxRunIDBytes = 64
)

const (
	memberListID      = "list_id"
	memberRouteDigest = "route_digest"
	memberRunID       = "run_id"
	memberThrough     = "through_line"
)

// Lift is the signed body of a lift: it ends every stop of one run on one
// list, under one route, up to and including one line of that list.
type Lift struct {
	Version     string
	ListID      string
	RouteDigest string
	RunID       string
	ThroughLine int64
}

// Payload is l's canonical bytes, the bytes a lift signature covers. It
// refuses a lift VerifyLift would not read back.
func (l Lift) Payload() ([]byte, error) {
	body, err := canon.Canonicalize(map[string]any{
		memberKind:        LiftKind,
		memberVersion:     l.Version,
		memberListID:      l.ListID,
		memberRouteDigest: l.RouteDigest,
		memberRunID:       l.RunID,
		memberThrough:     l.ThroughLine,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLiftValue, canon.ErrUnsupportedValue)
	}
	if _, err := readLift(body); err != nil {
		return nil, err
	}
	return body, nil
}

// readLift reads a lift's body, which must be its own canonical form.
func readLift(body []byte) (Lift, error) {
	if n := len(body); n > MaxLiftBytes {
		return Lift{}, fmt.Errorf("%w: %d bytes, limit %d", ErrLiftTooLarge, n, MaxLiftBytes)
	}
	names := []string{memberKind, memberVersion, memberListID, memberRouteDigest, memberRunID, memberThrough}
	refusals := docRefusals{json: ErrLiftJSON, repeated: ErrLiftRepeated, kind: ErrLiftKind, member: ErrLiftMember}
	o, err := readDoc(body, LiftKind, refusals, names, names)
	if err != nil {
		return Lift{}, err
	}
	var l Lift
	var ok bool
	if l.Version, ok = strictjson.String(o[memberVersion]); !ok || l.Version != LiftVersion {
		return Lift{}, ErrLiftVersion
	}
	if l.ListID, ok = identifier(o[memberListID], MaxListIDBytes); !ok {
		return Lift{}, fmt.Errorf("%w: %s", ErrLiftValue, memberListID)
	}
	if l.RouteDigest, ok = strictjson.String(o[memberRouteDigest]); !ok || !canon.ValidDigest(l.RouteDigest) {
		return Lift{}, fmt.Errorf("%w: %s", ErrLiftValue, memberRouteDigest)
	}
	if l.RunID, ok = identifier(o[memberRunID], MaxRunIDBytes); !ok {
		return Lift{}, fmt.Errorf("%w: %s", ErrLiftValue, memberRunID)
	}
	if l.ThroughLine, ok = integer(o[memberThrough], 1, policy.MaxSerial); !ok {
		return Lift{}, ErrLiftLine
	}
	canonical, err := canonicalOf(body, ErrLiftJSON)
	if err != nil {
		return Lift{}, err
	}
	if !bytes.Equal(canonical, body) {
		return Lift{}, ErrLiftNotCanonical
	}
	return l, nil
}
