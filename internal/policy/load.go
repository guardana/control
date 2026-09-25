package policy

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/internal/policy/rules"
)

// Load runs every check a bundle has to pass and returns the snapshot of the
// signed document, confirmed current at now. Load reads no clock. The checks
// run in this order, and the first that fails is returned, each with its own
// refusal:
//
//  1. the bundle, its ref and the ref's created_at carry no field this build
//     does not know (ErrUnknownField);
//  2. signature_alg is bundle.SignatureAlg (ErrSignatureAlg);
//  3. key_id selects a pinned key of the right size (ErrKey);
//  4. the signature verifies over the DSSE encoding of canonical (ErrSignature);
//  5. canonical parses as a policy document, and compiles (ErrDocument);
//  6. canonical is its own canonical form (ErrNotCanonical);
//  7. ref.digest is the digest of canonical (ErrDigest);
//  8. ref.bundle_id, ref.version and max_stale_seconds are the document's
//     (ErrMismatch);
//  9. ref.created_at is unset (ErrCreatedAt).
//
// A nil bundle is ErrNoBundle. Ahead of check 1, and before the bundle is
// copied, a canonical longer than the parser's bound of 1 MiB is ErrTooLarge,
// so a sender with no key costs no copy and no hash. Check 1 sees only the
// unknown fields the decoder kept: a decoder that discards them makes it
// blind.
func Load(b *controlv1.PolicyBundle, keys bundle.Keyring, now time.Time) (*Snapshot, error) {
	return load(b, keys, now, match.Compile)
}

// maxCanonicalBytes is the parser's own bound on a document, held here so an
// oversized body is refused before it is copied or hashed.
const maxCanonicalBytes = 1 << 20

// compiler is match.Compile, except in a test that needs one that refuses a
// document Parse accepted, or returns no program at all.
type compiler func(*rules.Document) (*match.Program, error)

func load(b *controlv1.PolicyBundle, keys bundle.Keyring, now time.Time, compile compiler) (*Snapshot, error) {
	if b == nil {
		return nil, ErrNoBundle
	}
	if n := len(b.GetCanonical()); n > maxCanonicalBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, n, maxCanonicalBytes)
	}
	// The caller's message is copied once, here, and only its length was read
	// before. The signature is checked over the copy's canonical and that same
	// slice is parsed, so no later write to the caller's message can part the
	// body verified from the body used.
	c := proto.CloneOf(b)
	if err := checkWrapper(c); err != nil {
		return nil, err
	}
	if err := verify(c, keys); err != nil {
		return nil, err
	}
	doc, program, err := readDocument(c.GetCanonical(), compile)
	if err != nil {
		return nil, err
	}
	digest := bundle.Digest(c.GetCanonical())
	if err := checkRef(c, doc, digest); err != nil {
		return nil, err
	}
	return &Snapshot{
		ref:         &controlv1.PolicyBundleRef{BundleId: doc.Bundle.ID, Version: doc.Bundle.Version, Digest: digest},
		confirmedAt: now,
		// Parse refuses a budget with more seconds than a Duration holds, so
		// this cannot wrap.
		maxStale: time.Duration(doc.Bundle.MaxStaleSeconds) * time.Second,
		serial:   doc.Bundle.Serial,
		program:  program,
	}, nil
}

// checkWrapper runs the two checks that read nothing signed.
func checkWrapper(c *controlv1.PolicyBundle) error {
	for _, m := range []struct {
		name    string
		message proto.Message
	}{
		{"the bundle", c},
		{"ref", c.GetRef()},
		{"ref.created_at", c.GetRef().GetCreatedAt()},
	} {
		if r := m.message.ProtoReflect(); r.IsValid() && len(r.GetUnknown()) > 0 {
			return fmt.Errorf("%w: on %s", ErrUnknownField, m.name)
		}
	}
	if c.GetSignatureAlg() != bundle.SignatureAlg {
		return ErrSignatureAlg
	}
	return nil
}

// verify runs the key and signature checks, which the bundle package makes,
// and gives each its own refusal.
func verify(c *controlv1.PolicyBundle, keys bundle.Keyring) error {
	err := bundle.VerifyBytes(c.GetCanonical(), c.GetSignature(), c.GetKeyId(), keys)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bundle.ErrUnknownKey), errors.Is(err, bundle.ErrKeySize), errors.Is(err, bundle.ErrWeakKey):
		// Each leaves no pinned key this build can use.
		return fmt.Errorf("%w: %w", ErrKey, err)
	default:
		// The signature's own refusal, and any refusal the bundle package may
		// add later: a verification that did not pass is not a key problem.
		return fmt.Errorf("%w: %w", ErrSignature, err)
	}
}

// readDocument runs the checks on the verified bytes themselves.
func readDocument(canonical []byte, compile compiler) (*rules.Document, *match.Program, error) {
	doc, canonicalForm, err := rules.Parse(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrDocument, err)
	}
	program, err := compile(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrDocument, err)
	}
	// A program nobody compiled answers as if no bundle were loaded, which a
	// decision made with a snapshot must never say.
	if program == nil {
		return nil, nil, fmt.Errorf("%w: the compiler returned no program", ErrDocument)
	}
	if !bytes.Equal(canonicalForm, canonical) {
		return nil, nil, ErrNotCanonical
	}
	return doc, program, nil
}

// checkRef holds what names the bundle outside canonical to the signed
// document.
func checkRef(c *controlv1.PolicyBundle, doc *rules.Document, digest string) error {
	ref := c.GetRef()
	if ref.GetDigest() != digest {
		return ErrDigest
	}
	switch {
	case ref.GetBundleId() != doc.Bundle.ID:
		return fmt.Errorf("%w: ref.bundle_id", ErrMismatch)
	case ref.GetVersion() != doc.Bundle.Version:
		return fmt.Errorf("%w: ref.version", ErrMismatch)
	case c.GetMaxStaleSeconds() != doc.Bundle.MaxStaleSeconds:
		return fmt.Errorf("%w: max_stale_seconds", ErrMismatch)
	}
	if ref.GetCreatedAt() != nil {
		return ErrCreatedAt
	}
	return nil
}
