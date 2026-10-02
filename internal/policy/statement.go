package policy

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/strictjson"
)

const (
	// StatementKind is the one kind a freshness statement's body may name.
	StatementKind = "agent-policy-freshness/v1alpha1"

	// MaxStatementBytes bounds a statement's body as VerifyStatement is
	// handed it. A body read from a file is held first to the file's bound,
	// policykey.MaxStatementFileBytes, which is this one: base64 makes the
	// body at most three quarters of the file, so this bound binds only a
	// caller that hands VerifyStatement parts it did not read from a file.
	MaxStatementBytes = 4096

	// issuedAtLayout is the one spelling of issuedAt. time.Parse takes a
	// fraction after the seconds that the layout does not name, so a value is
	// taken only when it formats back to itself.
	issuedAtLayout = "2006-01-02T15:04:05Z"
)

// The members of a statement's body, each required.
const (
	memberKind     = "kind"
	memberBundleID = "bundleId"
	memberSerial   = "serial"
	memberDigest   = "digest"
	memberIssuedAt = "issuedAt"
)

// Statement is a verified freshness statement: the bundle it names and the
// signed time at which that bundle was current. Only VerifyStatement makes
// one, so holding a Statement means its signature and its body were checked.
type Statement struct {
	bundleID string
	serial   int64
	digest   string
	issuedAt time.Time
}

// BundleID is the id of the bundle the statement names.
func (s Statement) BundleID() string { return s.bundleID }

// Serial is the serial of the bundle the statement names.
func (s Statement) Serial() int64 { return s.serial }

// Digest is the digest of the bundle the statement names.
func (s Statement) Digest() string { return s.digest }

// IssuedAt is when the signer says the bundle was current, in UTC and to the
// second.
func (s Statement) IssuedAt() time.Time { return s.issuedAt }

// StatementSignature is one signature of a DSSE envelope: the keyid, which
// only selects a pinned key, and the raw signature bytes.
type StatementSignature struct {
	KeyID     string
	Signature []byte
}

// StatementEnvelope is a DSSE envelope's parts as bytes, decoded from the file
// outside this package.
type StatementEnvelope struct {
	PayloadType string
	Payload     []byte
	Signatures  []StatementSignature
}

// VerifyStatement verifies env under the pinned freshness keys and reads its
// body. The checks run in this order, the first that fails returned:
//
//  1. the payload type is bundle.StatementPayloadType (ErrStatementPayloadType);
//  2. there is exactly one signature (ErrStatementSignatures);
//  3. the body is at most MaxStatementBytes (ErrStatementTooLarge);
//  4. the keyid selects a usable pinned key (ErrStatementKey);
//  5. the signature verifies over the pre-authentication encoding of the
//     statement type and the body (ErrStatementSignature);
//  6. the body is a statement (see the ErrStatement sentinels).
//
// The signature is checked over the fixed type, never over the type the
// envelope names, so only check 1 compares the envelope's.
func VerifyStatement(env StatementEnvelope, freshnessKeys bundle.Keyring) (Statement, error) {
	if env.PayloadType != bundle.StatementPayloadType {
		return Statement{}, ErrStatementPayloadType
	}
	if n := len(env.Signatures); n != 1 {
		return Statement{}, fmt.Errorf("%w: %d", ErrStatementSignatures, n)
	}
	if n := len(env.Payload); n > MaxStatementBytes {
		return Statement{}, fmt.Errorf("%w: %d bytes, limit %d", ErrStatementTooLarge, n, MaxStatementBytes)
	}
	// One copy is verified and that same copy is read, so a write to the
	// caller's slice cannot part the body verified from the body read.
	body := bytes.Clone(env.Payload)
	sig := env.Signatures[0]
	err := bundle.VerifyBytesAs(bundle.StatementPayloadType, body, sig.Signature, sig.KeyID, freshnessKeys)
	switch {
	case err == nil:
	case errors.Is(err, bundle.ErrUnknownKey), errors.Is(err, bundle.ErrKeySize), errors.Is(err, bundle.ErrWeakKey):
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementKey, err)
	default:
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementSignature, err)
	}
	return readStatement(body)
}

// readStatement reads a statement body. A member is known by its exact name
// only, and each is read from the bytes it was written as, so no decoder
// folds a case, keeps the last of two, or reads null as empty.
func readStatement(body []byte) (Statement, error) {
	members, err := strictjson.ReadObject(body)
	switch {
	case errors.Is(err, strictjson.ErrRepeated):
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementRepeatedMember, err)
	case err != nil:
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementJSON, err)
	}
	// The canonical form refuses what the reader above passes in silence:
	// invalid UTF-8, an unpaired surrogate, an integer outside the JSON-safe
	// range.
	if _, err := canon.CanonicalizeJSON(body); err != nil {
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementJSON, statementJSONCause(err))
	}
	if kind, ok := members[memberKind]; ok && !isString(kind, StatementKind) {
		return Statement{}, ErrStatementKind
	}
	names := []string{memberKind, memberBundleID, memberSerial, memberDigest, memberIssuedAt}
	if err := members.Only(names...); err != nil {
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementUnknownMember, err)
	}
	if err := members.Require(names...); err != nil {
		return Statement{}, fmt.Errorf("%w: %w", ErrStatementMissingMember, err)
	}
	return statementValues(members)
}

// statementJSONCause keeps the canonical form's sentinel and drops its text,
// which can name a member by a pointer.
func statementJSONCause(err error) error {
	for _, cause := range []error{canon.ErrUnsupportedValue, canon.ErrTooDeep} {
		if errors.Is(err, cause) {
			return cause
		}
	}
	return errors.New("not well-formed JSON")
}

// statementValues reads the five members' values, each held to its form.
func statementValues(members strictjson.Object) (Statement, error) {
	id, ok := strictjson.String(members[memberBundleID])
	if !ok || !isBundleID(id) {
		return Statement{}, fmt.Errorf("%w: %s", ErrStatementValue, memberBundleID)
	}
	serial, err := strconv.ParseInt(string(members[memberSerial]), 10, 64)
	if err != nil || serial < 1 {
		return Statement{}, fmt.Errorf("%w: %s", ErrStatementValue, memberSerial)
	}
	digest, ok := strictjson.String(members[memberDigest])
	if !ok || !canon.ValidDigest(digest) {
		return Statement{}, fmt.Errorf("%w: %s", ErrStatementValue, memberDigest)
	}
	spelled, ok := strictjson.String(members[memberIssuedAt])
	if !ok {
		return Statement{}, ErrStatementIssuedAt
	}
	issuedAt, err := ParseIssuedAt(spelled)
	if err != nil {
		return Statement{}, err
	}
	return Statement{bundleID: id, serial: serial, digest: digest, issuedAt: issuedAt}, nil
}

func isString(raw json.RawMessage, want string) bool {
	s, ok := strictjson.String(raw)
	return ok && s == want
}

// SignStatement writes the body of a statement naming the bundle id, serial
// and digest, issued at issuedAt, and signs it with key under keyID. The body
// is read back as VerifyStatement reads it, so a statement no plane would take
// is never returned: an issuedAt with a fraction of a second, or the zero
// time, is refused rather than rounded. An empty keyID is ErrKey, and a key
// bundle.SignBytesAs refuses is ErrSigningKey.
func SignStatement(bundleID string, serial int64, digest string, issuedAt time.Time, key ed25519.PrivateKey, keyID string) (StatementEnvelope, error) {
	if keyID == "" {
		return StatementEnvelope{}, fmt.Errorf("%w: %w", ErrKey, bundle.ErrUnknownKey)
	}
	if issuedAt.Nanosecond() != 0 {
		return StatementEnvelope{}, fmt.Errorf("%w: a fraction of a second", ErrStatementIssuedAt)
	}
	body, err := canon.Canonicalize(map[string]any{
		memberKind:     StatementKind,
		memberBundleID: bundleID,
		memberSerial:   serial,
		memberDigest:   digest,
		memberIssuedAt: FormatIssuedAt(issuedAt),
	})
	if err != nil {
		return StatementEnvelope{}, fmt.Errorf("%w: %w", ErrStatementValue, statementJSONCause(err))
	}
	if _, err := readStatement(body); err != nil {
		return StatementEnvelope{}, err
	}
	signature, err := bundle.SignBytesAs(bundle.StatementPayloadType, body, key)
	if err != nil {
		return StatementEnvelope{}, fmt.Errorf("%w: %w", ErrSigningKey, err)
	}
	return StatementEnvelope{
		PayloadType: bundle.StatementPayloadType,
		Payload:     body,
		Signatures:  []StatementSignature{{KeyID: keyID, Signature: signature}},
	}, nil
}

// ParseIssuedAt reads issuedAt's one spelling, YYYY-MM-DDTHH:MM:SSZ, and
// refuses every other with ErrStatementIssuedAt, and any time before
// 1970-01-01T00:00:00Z: the zero time is no confirmation at all, and a time
// near it is outside what a decision's policy_loaded_at can carry.
func ParseIssuedAt(s string) (time.Time, error) {
	t, err := time.Parse(issuedAtLayout, s)
	if err != nil || t.Format(issuedAtLayout) != s || t.Before(time.Unix(0, 0)) {
		return time.Time{}, ErrStatementIssuedAt
	}
	return t, nil
}

// FormatIssuedAt is issuedAt's spelling of t in UTC, to the second.
func FormatIssuedAt(t time.Time) string {
	return t.UTC().Format(issuedAtLayout)
}
