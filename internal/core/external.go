package core

import "github.com/guardana/control/internal/policy/match"

// External is the external decision point's answer about one call, as the
// enforcement point hands it to Decide. Its state is unexported and comes
// only from the constructors below, so a caller can neither hand in a state
// the kernel does not know nor spell the code a decision carries for it. The
// zero value is "not asked".
type External struct {
	state externalState
}

type externalState uint8

const (
	notAsked externalState = iota
	allowed
	denied
	deniedObligations
	timedOut
	unavailable
	answerRefused
)

// ExternalAllowed is an answer that does not deny the call.
func ExternalAllowed() External { return External{allowed} }

// ExternalDenied is an answer that denies the call.
func ExternalDenied() External { return External{denied} }

// ExternalDeniedObligations is an answer that allowed the call only with an
// obligation this plane cannot fulfil, which makes it a denial.
func ExternalDeniedObligations() External { return External{deniedObligations} }

// ExternalTimeout is no answer within the deadline.
func ExternalTimeout() External { return External{timedOut} }

// ExternalUnavailable is no decision at all: the question could not be asked,
// or what came back was not an answer.
func ExternalUnavailable() External { return External{unavailable} }

// ExternalAnswerRefused is a reply this plane will not read as a decision.
func ExternalAnswerRefused() External { return External{answerRefused} }

// String names the state, for a log line or a test failure.
func (e External) String() string {
	switch e.state {
	case allowed:
		return "allowed"
	case denied:
		return "denied"
	case deniedObligations:
		return "denied with obligations"
	case timedOut:
		return "timeout"
	case unavailable:
		return "unavailable"
	case answerRefused:
		return "answer refused"
	}
	return "not asked"
}

// input is the answer as the external constraint reads it. Every state but
// an answer is unknown, so silence can leave a veto undetermined and never
// lift it.
func (e External) input() match.External {
	switch e.state {
	case allowed:
		return match.ExternalAllows
	case denied, deniedObligations:
		return match.ExternalDenies
	}
	return match.ExternalUnknown
}

// code is the one code a decision that consulted the answer carries, and ""
// when there was no answer to consult.
func (e External) code() string {
	switch e.state {
	case allowed:
		return codePDPAllow
	case denied:
		return codePDPDeny
	case deniedObligations:
		return codeObligationNotUnderstood
	case timedOut:
		return codePDPTimeout
	case unavailable:
		return codePDPUnavailable
	case answerRefused:
		return codePDPAnswerRefused
	}
	return ""
}
