package contract

import (
	"fmt"
	"strings"
)

// CheckHost refuses a string no destination.host may hold (ADR-0011). A policy
// matches a host as written, so a DENY on paste.example.net would be dodged by
// PASTE.EXAMPLE.NET, by paste.example.net. and by a Unicode name beside its
// A-label. Refused are a byte outside [a-z0-9.:-], a dot at either
// end, an empty label, and a colon anywhere but in an IPv6 literal, which holds
// at least two and nothing but [0-9a-f:.]; a port after a name is refused with
// them. No literal is normalized, so 0x7f.0.0.1 stays a second spelling of
// 127.0.0.1.
//
// Validate holds destination.host to this rule. It is exported so that anything
// else that names a host, the policy parser first, holds it to this rule rather
// than to a second one (docs/contracts.md, "Decoding and validation"): a rule
// that names a spelling no envelope can carry never matches, and a DENY on it
// never fires.
//
// Like CheckIdentifier it checks what a value holds, not whether there is one:
// "" passes. The refusal is a *ValidationError with no Field, wrapping
// ErrInvalidValue. It names a byte by its position and never repeats the host.
func CheckHost(host string) error {
	if err := hostProblem(host); err != nil {
		return &ValidationError{Err: err}
	}
	return nil
}

// hostProblem is CheckHost's rule without the wrapper, so checkValues can name
// the field.
func hostProblem(host string) error {
	if host == "" {
		return nil
	}
	for i := range len(host) {
		if !isHostByte(host[i]) {
			return fmt.Errorf("%w: a byte outside [a-z0-9.:-] at byte %d", ErrInvalidValue, i)
		}
	}
	switch {
	case host[0] == '.' || host[len(host)-1] == '.':
		return fmt.Errorf("%w: starts or ends with a dot", ErrInvalidValue)
	case strings.Contains(host, ".."):
		return fmt.Errorf("%w: an empty label", ErrInvalidValue)
	case strings.Contains(host, ":"):
		return ipv6Problem(host)
	}
	return nil
}

// ipv6Problem holds a host that carries a colon to an IPv6 literal (ruling
// R33a): at least two colons, and nothing but hex digits, dots and colons.
// Otherwise a port after a name or an IPv4 address would be one more spelling
// of it.
func ipv6Problem(host string) error {
	if strings.Count(host, ":") < 2 {
		return fmt.Errorf("%w: a colon outside an IPv6 literal", ErrInvalidValue)
	}
	for i := range len(host) {
		if !isIPv6Byte(host[i]) {
			return fmt.Errorf("%w: a byte outside [0-9a-f:.] in an IPv6 literal at byte %d", ErrInvalidValue, i)
		}
	}
	return nil
}

func isHostByte(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '.' || c == ':' || c == '-'
}

func isIPv6Byte(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || c == ':' || c == '.'
}
