package policy

import "github.com/guardana/control/internal/policy/bundle"

// Error is a refusal by Sign, Load or Install, matched with errors.Is. Each
// check Load runs has its own. They are constants, so no other code in the
// binary can reassign one and turn a refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// Load's refusals, one per check, in the order Load runs the checks.
const (
	// ErrNoBundle is a nil bundle.
	ErrNoBundle Error = "policy: no bundle"
	// ErrTooLarge is a canonical over the parser's bound of 1 MiB, refused
	// before the bundle is copied or hashed.
	ErrTooLarge Error = "policy: canonical is over a bound"
	// ErrUnknownField is a field this build does not know, on the bundle, its
	// ref or the ref's created_at. The runtime keeps a known field sent with
	// another wire type among the unknown ones, so that is refused too.
	ErrUnknownField Error = "policy: the bundle carries a field this build does not know"
	// ErrSignatureAlg is any signature_alg but the one value it may hold.
	ErrSignatureAlg Error = "policy: signature_alg is not " + bundle.SignatureAlg
	// ErrKey is a key_id that selects no pinned key this build can use: an
	// unknown or empty id, a key of the wrong size or a key of small order. It
	// wraps the bundle package's own refusal.
	ErrKey Error = "policy: key_id selects no usable pinned key"
	// ErrSignature is a signature that does not verify. It wraps the bundle
	// package's own refusal.
	ErrSignature Error = "policy: the signature does not verify"
	// ErrDocument is canonical bytes that are not a policy document the kernel
	// can run. It wraps the refusal of the parse or of the compile.
	ErrDocument Error = "policy: canonical is not a valid policy document"
	// ErrNotCanonical is a document that is not its own canonical form, which
	// would give one policy two digests.
	ErrNotCanonical Error = "policy: canonical is not its own canonical form"
	// ErrDigest is a ref.digest that is not the digest of canonical.
	ErrDigest Error = "policy: ref.digest is not the digest of canonical"
	// ErrMismatch is a ref.bundle_id, ref.version or max_stale_seconds that
	// differs from what the signed document says.
	ErrMismatch Error = "policy: a field outside canonical differs from the document"
	// ErrCreatedAt is a ref.created_at that is set: no creation time is signed,
	// so any value there is the sender's word alone.
	ErrCreatedAt Error = "policy: ref.created_at is set, and no creation time is signed"
)

// ErrSigningKey is Sign's refusal of a private key that cannot sign: one of the
// wrong size, or one whose public half is not its seed's. It wraps the bundle
// package's own refusal.
const ErrSigningKey Error = "policy: the private key cannot sign"

// Install's refusals of a bundle that passed every check of Load.
const (
	// ErrRollback is a serial below one installed before for its bundle id.
	ErrRollback Error = "policy: a serial below one installed for this bundle id"
	// ErrSerialReused is a serial installed before for its bundle id, with
	// another digest.
	ErrSerialReused Error = "policy: a serial installed for this bundle id with another digest"
	// ErrBundlePin is a bundle whose id is not the one the Holder was pinned
	// to.
	ErrBundlePin Error = "policy: the bundle id is not the one this holder is pinned to"
)
