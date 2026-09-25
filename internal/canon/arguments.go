package canon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
)

// ArgumentsHashV1 returns the arguments hash: "sha256:" and the lowercase hex
// of sha256 over the arguments tag followed by the canonical form of the
// authorized arguments, "{}" when there are none (ADR-0011). A producer writes
// it into arguments.canonical_hash, and the receiver, which holds the
// arguments, recomputes it and refuses a mismatch. It reads the document
// through the path DigestV1 takes, with the same bound and the same refusals,
// so the two never disagree about which documents exist.
func ArgumentsHashV1(authorizedArgs []byte) (string, error) {
	body, err := authorizedArguments(authorizedArgs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(slices.Concat([]byte(ArgumentsDomainV1), []byte(body)))
	return digestPrefix + hex.EncodeToString(sum[:]), nil
}

// canonical is a value already in canonical form, which the encoder writes as
// it stands. Only this package makes one, from Canonicalize's own output, so
// the encoder has nothing left to check in it.
type canonical []byte

// authorizedArguments returns the canonical bytes of the authorized arguments:
// the value the action digest holds under authorizedArguments, from the one
// path every use of the arguments takes. A float, an oversized integer, a
// duplicate key or an unpaired surrogate in a tool call is refused here rather
// than hashed, and a JSON pointer in the refusal is relative to the arguments
// document, not to the canonical action.
//
// The document is canonicalized on its own, so its containers are counted from
// its own root, as the parser counts them. Embedded as a value, it would count
// the action object around it as one of its 32.
//
// Absent arguments are the empty object. A document that is the literal null is
// refused instead of being read as absent: ADR-0005 says no field of the set is
// ever null, and two spellings of "no arguments" that hash differently is the
// cross-language disagreement the record exists to prevent. A null nested
// inside the arguments is ordinary data and is kept.
func authorizedArguments(raw []byte) (canonical, error) {
	if len(raw) > MaxArgumentsBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrArgumentsTooLarge, len(raw), MaxArgumentsBytes)
	}
	if len(raw) == 0 {
		return canonical("{}"), nil
	}
	v, err := parseJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", argumentsKey, err)
	}
	if v == nil {
		// The same shape as every other refusal about this document: the field,
		// then the pointer, which is the root of the arguments themselves.
		return nil, fmt.Errorf("%s: %w at %q: the document is null; absent arguments are the empty object",
			argumentsKey, ErrUnsupportedValue, "")
	}
	body, err := Canonicalize(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", argumentsKey, err)
	}
	return canonical(body), nil
}
