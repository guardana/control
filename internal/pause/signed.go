package pause

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy"
)

// SignedVersion is the version of a pause file an operator signs: a DSSE
// envelope of PayloadType over a body that names the signing key, the
// plane's tenant and project, a serial and the entries.
const SignedVersion = "3"

// PayloadType is the DSSE payload type a pause document is signed under,
// operators.PausePayloadType; this package links no operator port, and a
// test holds the two equal.
const PayloadType = "application/vnd.agent-pause+json"

// MaxSignedBodyBytes bounds a signed document's body at the operator
// authenticator's bound on any statement, which a test holds it to.
const MaxSignedBodyBytes = 16384

// signedAlg is the one algorithm a signed body names: an operator key's.
const signedAlg = "ed25519"

// signedMembers is every member of a signed body, each required.
var signedMembers = []string{"alg", "entries", "keyId", "project", "schema_version", "serial", "tenant"}

// Header is what a signed pause document says besides its entries: the key
// that signed it, the tenant and project it pauses in, and its serial, which
// only ever rises.
type Header struct {
	KeyID           string
	Tenant, Project string
	Serial          int64
}

// SignedDocument is a signed pause document as its body holds it, judged by
// nothing yet: its digest is over the body as signed.
type SignedDocument struct {
	Header
	Document
	Digest string
}

// Signer is an operator key a writer signs with: its id, and a function that
// signs a body under a payload type.
type Signer struct {
	KeyID string
	Sign  func(payloadType string, body []byte) ([]byte, error)
}

// file is a pause file as read: its document, and for a signed one the
// envelope and its body's header.
type file struct {
	doc    Document
	signed *Signed
}

// Signed is a signed file as read: its envelope, and its body as ParseSigned
// read it. Nothing in it is judged.
type Signed struct {
	Envelope policy.StatementEnvelope
	Body     SignedDocument
}

// decodeFile reads raw as a pause file of any version: one object naming a
// schema_version is version 1 or 2, and one naming a payloadType is a signed
// envelope, whose body is read with ParseSigned. The envelope's signature is
// not checked here.
func decodeFile(raw []byte) (file, error) {
	if len(raw) > MaxFileBytes {
		return file{}, fmt.Errorf("%w: %d bytes, the bound is %d", ErrTooLarge, len(raw), MaxFileBytes)
	}
	top, err := members(raw, true)
	if err != nil {
		return file{}, malformed("%v", err)
	}
	if _, signed := top["payloadType"]; !signed {
		doc, err := Parse(raw)
		return file{doc: doc}, err
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		return file{}, malformed("the envelope: %v", err)
	}
	switch {
	case env.PayloadType != PayloadType:
		return file{}, malformed("the envelope's payload type is not %s", PayloadType)
	case len(env.Signatures) != 1:
		return file{}, malformed("the envelope holds %d signatures, not one", len(env.Signatures))
	}
	body, err := ParseSigned(env.Payload)
	if err != nil {
		return file{}, err
	}
	if body.KeyID != env.Signatures[0].KeyID {
		return file{}, malformed("the body names key %s and the signature key %s", body.KeyID, bounded(env.Signatures[0].KeyID))
	}
	return file{doc: body.Document, signed: &Signed{Envelope: env, Body: body}}, nil
}

// ParseSigned reads a signed pause body strictly: at most MaxSignedBodyBytes,
// its own canonical JSON, every member of the format and no other,
// schema_version SignedVersion, alg ed25519, a key id, the tenant and the
// project within the identifier rule, a serial from 1 to policy.MaxSerial,
// and entries by the rules of a version 2 file.
func ParseSigned(body []byte) (SignedDocument, error) {
	if len(body) > MaxSignedBodyBytes {
		return SignedDocument{}, fmt.Errorf("%w: a signed body of %d bytes, the bound is %d", ErrTooLarge, len(body), MaxSignedBodyBytes)
	}
	canonical, err := canon.CanonicalizeJSON(body)
	if err != nil || !bytes.Equal(canonical, body) {
		return SignedDocument{}, malformed("the signed body is not its own canonical form")
	}
	top, err := members(body, true)
	if err != nil {
		return SignedDocument{}, malformed("%v", err)
	}
	if err := only(top, signedMembers...); err != nil {
		return SignedDocument{}, malformed("%v", err)
	}
	s := SignedDocument{Digest: digestOf(body)}
	if s.Header, err = readHeader(top); err != nil {
		return SignedDocument{}, err
	}
	list, err := member(top, "entries", array)
	if err != nil {
		return SignedDocument{}, malformed("%v", err)
	}
	if len(list) > MaxEntries {
		return SignedDocument{}, malformed("%d entries, the bound is %d", len(list), MaxEntries)
	}
	if s.Document, err = parseEntries(list, SchemaVersion); err != nil {
		return SignedDocument{}, err
	}
	if err := s.Check(); err != nil {
		return SignedDocument{}, err
	}
	return s, nil
}

// readHeader reads every member of a signed body but its entries: the
// version, which has to be SignedVersion, the algorithm, the key id, the
// tenant, the project and the serial.
func readHeader(top map[string]json.RawMessage) (Header, error) {
	version, err := member(top, "schema_version", str)
	if err != nil {
		return Header{}, malformed("%v", err)
	}
	if version != SignedVersion {
		return Header{}, fmt.Errorf("%w: a signed body of %q, this build signs %q", ErrVersion, bounded(version), SignedVersion)
	}
	var h Header
	for name, to := range map[string]*string{"keyId": &h.KeyID, "tenant": &h.Tenant, "project": &h.Project} {
		if *to, err = member(top, name, str); err != nil {
			return Header{}, malformed("%v", err)
		}
	}
	alg, err := member(top, "alg", str)
	if err != nil {
		return Header{}, malformed("%v", err)
	}
	if h.Serial, err = member(top, "serial", serial); err != nil {
		return Header{}, malformed("%v", err)
	}
	if err := h.check(); err != nil {
		return Header{}, err
	}
	if alg != signedAlg {
		return Header{}, malformed("alg: not %s", signedAlg)
	}
	return h, nil
}

// check refuses a header whose key id, tenant or project is empty, past its
// bound or outside the identifier rule, and a serial outside 1 to
// policy.MaxSerial.
func (h Header) check() error {
	if err := checkName("keyId", h.KeyID, MaxIDBytes); err != nil {
		return malformed("%v", err)
	}
	if err := checkName("tenant", h.Tenant, MaxNameBytes); err != nil {
		return malformed("%v", err)
	}
	if err := checkName("project", h.Project, MaxNameBytes); err != nil {
		return malformed("%v", err)
	}
	if h.Serial < 1 || h.Serial > policy.MaxSerial {
		return malformed("serial: %d is outside 1 to %d", h.Serial, int64(policy.MaxSerial))
	}
	return nil
}

// MarshalSigned is the canonical body of a signed document of h over d,
// refused where ParseSigned would not read it back as h and d.
func MarshalSigned(h Header, d Document) ([]byte, error) {
	if err := h.check(); err != nil {
		return nil, err
	}
	if err := d.Check(); err != nil {
		return nil, err
	}
	entries := make([]any, 0, len(d.Entries))
	for _, e := range d.Entries {
		scope := map[string]any{"kind": string(e.Scope.Kind)}
		for name, v := range map[string]string{"provider": e.Scope.Provider, "action": e.Scope.Action, "name": e.Scope.Name, "id": e.Scope.ID} {
			if v != "" {
				scope[name] = v
			}
		}
		entries = append(entries, map[string]any{
			"id": e.ID, "scope": scope, "created_at": e.CreatedAt.UTC().Format(time.RFC3339Nano), "reason": e.Reason,
		})
	}
	body, err := canon.Canonicalize(map[string]any{
		"schema_version": SignedVersion, "alg": signedAlg, "keyId": h.KeyID,
		"tenant": h.Tenant, "project": h.Project, "serial": h.Serial, "entries": entries,
	})
	if err != nil {
		return nil, malformed("%v", err)
	}
	back, err := ParseSigned(body)
	if err != nil {
		return nil, err
	}
	if back.Header != h || !slices.EqualFunc(back.Entries, d.Entries, sameEntry) {
		return nil, malformed("the signed body does not read back as the document")
	}
	return body, nil
}

// signFile signs the body of h over d with s and returns the file's bytes.
func signFile(h Header, d Document, s Signer) ([]byte, error) {
	if s.Sign == nil || s.KeyID != h.KeyID {
		return nil, ErrSigner
	}
	body, err := MarshalSigned(h, d)
	if err != nil {
		return nil, err
	}
	sig, err := s.Sign(PayloadType, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSigner, err)
	}
	raw, err := marshalEnvelope(policy.StatementEnvelope{
		PayloadType: PayloadType, Payload: body,
		Signatures: []policy.StatementSignature{{KeyID: h.KeyID, Signature: sig}},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxFileBytes {
		return nil, fmt.Errorf("%w: %d bytes, the bound is %d", ErrTooLarge, len(raw), MaxFileBytes)
	}
	return raw, nil
}

// serial reads a JSON integer from 1 to policy.MaxSerial spelled in plain
// decimal digits.
func serial(raw json.RawMessage) (int64, error) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != string(raw) {
		return 0, fmt.Errorf("not an integer")
	}
	return n, nil
}

// digestOf is the digest the floor keeps of a signed body.
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
