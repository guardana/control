package trust

// Error is a refusal by this package, matched with errors.Is. The values are
// constants, so no code in the binary can set one to nil and read a refusal
// as a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The refusals of a roster's envelope and signature.
const (
	// ErrPayloadType is an envelope whose payload type is not the roster's.
	ErrPayloadType Error = "trust: the envelope's payload type is not the roster's"
	// ErrSignatures is an envelope holding any number of signatures but one.
	ErrSignatures Error = "trust: a roster carries exactly one signature"
	// ErrTooLarge is a roster body over MaxBodyBytes.
	ErrTooLarge Error = "trust: the roster is larger than its bound"
	// ErrRootKey is a signature whose keyid is not the pinned root's, or a
	// root that is no usable Ed25519 key.
	ErrRootKey Error = "trust: the roster is not signed by the pinned root key"
	// ErrSignature is a signature that does not verify under the root.
	ErrSignature Error = "trust: the roster's signature does not verify"
)

// The refusals of a roster's body.
const (
	// ErrMalformed is a body that is not one strict JSON object of the
	// roster's shape, or a member of the wrong type or form.
	ErrMalformed Error = "trust: the roster is malformed"
	// ErrNotCanonical is a body that is not its own canonical form.
	ErrNotCanonical Error = "trust: the roster is not its own canonical form"
	// ErrKind is a body naming a kind other than DocumentKind.
	ErrKind Error = "trust: the roster names a kind this build does not read"
	// ErrTimes is an issuedAt or expiresAt in another spelling, an expiry
	// not after the issue, or a lifetime over MaxLifetime.
	ErrTimes Error = "trust: the roster's issuedAt and expiresAt are not a lifetime it may have"
	// ErrKeyEntry is a key entry the roster may not carry: an unknown
	// algorithm or role, a key id that is not its key's, a key of small
	// order, or a subject outside its rule.
	ErrKeyEntry Error = "trust: a key entry of the roster is refused"
	// ErrKeyReused is one key named twice in a roster, or a roster key that
	// is the root or a key the plane pins for another authority.
	ErrKeyReused Error = "trust: one key stands for two authorities"
	// ErrGrant is a grant the roster may not carry: an empty or patterned
	// tenant or project, an unknown level, or one tenant and project
	// granted twice to one key.
	ErrGrant Error = "trust: a grant of the roster is refused"
	// ErrTooMany is a roster of more than MaxKeys keys, or a key of more
	// than MaxGrants grants.
	ErrTooMany Error = "trust: the roster holds more keys or grants than it may"
)

// The refusals of a roster's time and floor.
const (
	// ErrFuture is a roster issued after the clock that reads it.
	ErrFuture Error = "trust: the roster is dated after the clock"
	// ErrExpired is a roster read at or after its expiresAt.
	ErrExpired Error = "trust: the roster has expired"
)

// The refusals of the kind and its configuration, made once at start.
const (
	// ErrUnknownKind is a kind this build does not have.
	ErrUnknownKind Error = "trust: not a kind of trust roster this build has"
	// ErrConfig is a configuration the kind cannot start from.
	ErrConfig Error = "trust: the trust roster's configuration is refused"
)
