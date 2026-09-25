// Package reasons holds the closed set of reason codes a decision may carry.
//
// A reason code is the machine-readable answer to why a call received the
// verdict it did. It travels in Decision.reason_codes and into the evidence
// record, so the set is closed on purpose: a matcher cannot emit a code that
// nothing documents, and whoever reads a decision can look every code up.
//
// Code.Verdict records the verdict a code normally accompanies. It is
// documentation, not a decision function. A verdict comes from the matcher and
// from deny-overrides precedence (ADR-0003), never from the reason attached to
// it afterwards.
//
// What that means exactly, because a comment claiming more than the code does
// is itself a false green. Lookup returns the whole record, so
// Lookup(id).Verdict does hand a caller a verdict in one exported call, and
// nothing here can prevent that while the field exists. What is prevented is
// this package offering the shortcut as its own API and growing a second one:
// surface_test.go pins the exported functions to Lookup and All with their
// exact signatures, pins the exported types to Code with its exact fields, and
// fails on a Verdict mentioned anywhere else, a parameter or a second struct
// field included.
//
// That pin reaches this directory and no further. It cannot stop a sibling
// package from wrapping Lookup and returning a verdict, and it is not written
// as though it could: the guard that matters for a decision belongs in the
// matcher's own package, beside the code that reaches a verdict properly.
package reasons

// Lookup returns the registered code with this identifier.
//
// A miss returns the zero Code, whose Num is 0 and whose Verdict is
// VERDICT_UNSPECIFIED. The registry holds neither value, so a caller that
// ignores ok cannot mistake a miss for a code that means something.
func Lookup(id string) (Code, bool) {
	// A scan over a table this size costs less than the lookup in a map built
	// at init would, and it keeps the package free of state built at init.
	for _, code := range codes {
		if code.ID == id {
			return code, true
		}
	}
	return Code{}, false
}

// All returns every registered code, ordered by Num.
//
// The result is a fresh slice each call. Code holds only value fields, so the
// copy is complete: a caller that sorts, truncates or rewrites the result
// cannot change what the next caller sees.
func All() []Code {
	out := make([]Code, len(codes))
	copy(out, codes[:])
	return out
}
