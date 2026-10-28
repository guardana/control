package egresslog

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/guardana/control/internal/observe"
)

// maxLabelBytes bounds one label of a name, as DNS does.
const maxLabelBytes = 63

// canonicalHost reads a destination as a host in its one spelling, or
// reports false. A name is lowercased and held to LDH labels; an IPv4
// address is a dotted quad; an IPv6 address is written as net/netip writes
// it, with no zone and no brackets. None of these admits a scheme, a path, a
// query, a fragment, user information or whitespace, so a URL is refused
// whole, never parsed down to the host inside it. A name in U-labels is
// refused: mapping it to A-labels needs IDNA tables this build does not
// carry, and an A-label is taken as written.
func canonicalHost(s string) (string, bool) {
	// Checked before lowercasing, which maps some non-ASCII letters, the
	// Kelvin sign among them, to ASCII ones.
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return "", false
		}
	}
	if strings.Contains(s, ":") {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return "", false
		}
		return a.String(), true
	}
	s = strings.ToLower(s)
	if len(s) > observe.MaxHostBytes {
		return "", false
	}
	labels := strings.Split(s, ".")
	for _, l := range labels {
		if !ldh(l) {
			return "", false
		}
	}
	// A top-level label is never all digits, so a name ending in one is an
	// IPv4 address, which has no colon, or nothing: never a second spelling
	// of one.
	if digits(labels[len(labels)-1]) {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return "", false
		}
		return a.String(), true
	}
	return s, true
}

// ldh reports a label of 1 to 63 letters, digits and hyphens that neither
// starts nor ends with a hyphen. s is lowercase.
func ldh(s string) bool {
	if s == "" || len(s) > maxLabelBytes || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
