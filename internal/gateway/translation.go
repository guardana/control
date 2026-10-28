package gateway

import (
	"errors"

	"github.com/guardana/control/internal/channel"
	"github.com/guardana/control/pkg/contract"
)

// translation is how far an adapter could translate a call. The zero value
// is refused, so a state nobody set hands nothing out.
type translation uint8

const (
	// translationRefused: the adapter refused the call for something other
	// than nothing classifying it, or handed in no envelope. No mode hands it
	// out.
	translationRefused translation = iota
	// translationUnclassified: every part of the envelope was built but the
	// classification, which nothing gives. OBSERVE lets it run; every other
	// mode blocks it with ACTION_UNCLASSIFIED.
	translationUnclassified
	// translationComplete: the adapter refused nothing.
	translationComplete
)

// translationOf is the one place the plane says whether a call is
// unclassified; every reader takes it from here.
func translationOf(a Admission) translation {
	switch {
	case a.Envelope == nil:
		return translationRefused
	case a.Refusal == nil:
		return translationComplete
	case unclassifiedOnly(a.Refusal):
		return translationUnclassified
	}
	return translationRefused
}

// observable reports whether OBSERVE may hand the call out: nothing but its
// classification is missing, or the adapter refused nothing and the kernel
// took the request. An adapter that hands in an envelope the contract refuses
// and no refusal is held to the kernel's own refusal of it.
func (c *call) observable() bool {
	switch translationOf(c.in) {
	case translationUnclassified:
		return true
	case translationComplete:
		return !c.refusedInput
	}
	return false
}

// unclassifiedOnly reports whether err is ErrUnclassified and nothing else: the
// sentinel itself at the end of a chain of single wraps. A node that wraps
// several errors joins another cause with it, and the call is refused for
// that cause. A node's Is method is not consulted, since it could claim the
// sentinel over any cause.
func unclassifiedOnly(err error) bool {
	for err != nil {
		if sentinel, ok := err.(Error); ok { //nolint:errorlint // each node of the chain is read itself, so a wrap cannot hide a join
			return sentinel == ErrUnclassified
		}
		if _, joined := err.(interface{ Unwrap() []error }); joined {
			return false
		}
		err = errors.Unwrap(err)
	}
	return false
}

// refuseVocabulary refuses an envelope whose action kind is outside the
// plane's vocabulary, or whose protocol is not the adapter's name, in place
// of a refusal that says only that nothing classifies the call: otherwise
// OBSERVE would run a kind no pause scope names. A refusal for anything else
// stays the call's own.
func (c *call) refuseVocabulary() {
	if translationOf(c.in) == translationRefused {
		return
	}
	action := c.in.Envelope.GetAction()
	switch {
	case !channel.KnownKind(action.GetKind()):
		c.in.Refusal = &contract.ValidationError{Field: "action.kind", Err: contract.ErrInvalidValue}
	case action.GetProtocol() != c.p.name:
		c.in.Refusal = &contract.ValidationError{Field: "action.protocol", Err: contract.ErrInvalidValue}
	}
}
