package core

// The reason codes this package emits, as literals: nothing on the decision
// path reads the registry (ADR-0012), and codes_test.go holds each one to its
// entry and to the verdict of the step that emits it. The delegation codes are
// the delegation package's, copied from its typed refusal; the rule codes are
// the matcher's, copied from its result.
const (
	codeUnsupportedSchema       = "UNSUPPORTED_SCHEMA"
	codeRequiredFieldAbsent     = "REQUIRED_FIELD_ABSENT"
	codeLimitExceeded           = "LIMIT_EXCEEDED"
	codeInvalidFieldValue       = "INVALID_FIELD_VALUE"
	codeMalformedInput          = "MALFORMED_INPUT"
	codeTenantMismatch          = "TENANT_MISMATCH"
	codeTenantUndetermined      = "TENANT_UNDETERMINED"
	codePolicyUnavailable       = "POLICY_UNAVAILABLE"
	codePolicyStale             = "POLICY_STALE"
	codeObligationNotUnderstood = "OBLIGATION_NOT_UNDERSTOOD"
	codeFailOpenRead            = "FAIL_OPEN_READ_CONFIGURED"
	codePDPTimeout              = "PDP_TIMEOUT"
	codePDPUnavailable          = "PDP_UNAVAILABLE"
	codePDPAnswerRefused        = "PDP_ANSWER_REFUSED"
	codePDPDeny                 = "PDP_DENY"
	codePDPAllow                = "PDP_ALLOW"
)

// The fixed fields of every decision this kernel makes.
const (
	schemaVersion = "1.0"
	pdpType       = "builtin"
)
