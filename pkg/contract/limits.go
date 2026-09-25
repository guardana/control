// Package contract holds the size limits, the refusal errors and the
// validation the v1 wire messages are checked against.
//
// The generated Protobuf types are the types. This package builds no second
// type hierarchy over them and converts nothing; it adds the checks that let
// everything downstream assume a message is well formed and within bounds.
//
// Every non-nil error is a refusal and a *ValidationError, naming the field
// when one field is at fault. The sentinels in errors.go classify most of them;
// a payload that does not decode at all matches none, so a caller that switches
// on the sentinels needs a branch for the refusal it cannot classify. See
// docs/contracts.md.
package contract

// Bounds on a message nothing has authorized yet, which is the only kind of
// message this package sees. Each caps work a caller can make a receiver do
// before any policy runs. docs/contracts.md carries the same table, and a test
// in this package fails when the two disagree.
//
// Untyped on purpose: they are compared against an int from len or proto.Size
// and against an int64 read out of a message, and a typed constant would force
// a conversion at each site.
const (
	// MaxEnvelopeBytes caps one encoded ActionEnvelope at 256 KiB. It is the
	// only bound over the whole message, so it still holds where a caller
	// spreads its bytes across fields that have no limit of their own.
	MaxEnvelopeBytes = 262144

	// MaxArgumentsBytes caps the encoded Arguments message at 64 KiB.
	// Arguments are caller data, and the fields policy reads must not be
	// crowded out by the one field it does not.
	MaxArgumentsBytes = 65536

	// MaxDelegationDepth caps the hops in a delegation chain. Each hop is a
	// link a delegation check walks, and a chain longer than this is one no
	// reviewer reads either.
	MaxDelegationDepth = 8

	// MaxLabels caps the entries in one caller-supplied collection: resource
	// labels, principal attributes, the scopes of one delegation hop, run tags,
	// data sources. Every one of them is a policy input, and MaxEnvelopeBytes
	// is no bound on their count: tens of thousands of either fit inside it.
	MaxLabels = 32

	// MaxStringBytes caps a single string field, measured in bytes rather than
	// runes, because the limit bounds memory and one rune costs up to four
	// bytes.
	MaxStringBytes = 1024

	// MaxPreviewBytes caps arguments.redacted_preview, the one string a human
	// reads rather than matches on. Raising MaxStringBytes to fit a preview
	// would raise it for every identifier too, so the preview gets its own.
	MaxPreviewBytes = 4096

	// MaxNesting caps how deep a decode or a walk descends into nested
	// messages. Nothing in v1 can reach it: no v1 message is recursive and the
	// deepest chain is three, which this package's own test measures. It bounds
	// the walk rather than the schema, so that a recursive message added later, or a
	// message tree from somewhere else, cannot drive the walk until the stack
	// runs out.
	MaxNesting = 32
)
