package contract_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// TestCheckHostIsValidatesHostRule holds the exported check to Validate, as
// docs/contracts.md promises under "Decoding and validation". Validate's
// verdict on destination.host is the string bound, the identifier rule and
// CheckHost, nothing else, and where CheckHost refuses, Validate refuses for
// the same reason. A caller that holds a host named elsewhere to CheckHost,
// the policy parser first, then names one an envelope can carry, and a DENY
// on it can fire. The hosts are built from the bytes the rule names and their
// neighbours, so that both edges of every set are drawn.
func TestCheckHostIsValidatesHostRule(t *testing.T) {
	pieces := []string{
		"paste", "example", "net", "xn--", "bcher-kva", "192", "0", "2001", "db8", "ffff", "dead", "beef",
		".", ":", "::", "-", "PASTE", "Net", "g", "z", "_", " ", "/", "[", "]", "\U000000fc", "\U0000017f", "\U0000200b",
	}
	rapid.Check(t, func(rt *rapid.T) {
		host := rapid.OneOf(
			rapid.Map(rapid.SliceOfN(rapid.SampledFrom(pieces), 0, 8), func(p []string) string { return strings.Join(p, "") }),
			rapid.String(),
			rapid.Map(rapid.SliceOf(rapid.Byte()), func(b []byte) string { return string(b) }),
		).Draw(rt, "host")
		assertHostAgreement(rt, host)
	})

	// The bound, which no draw above reaches, and the empty host, which is an
	// absent one to both.
	for _, host := range []string{"", strings.Repeat("a", contract.MaxStringBytes), strings.Repeat("a", contract.MaxStringBytes+1)} {
		assertHostAgreement(t, host)
	}
}

// assertHostAgreement holds Validate's verdict on one host to the three rules
// that make it up, and where CheckHost refuses on its own, holds Validate to
// CheckHost's reason at destination.host.
func assertHostAgreement(t rapid.TB, host string) {
	t.Helper()
	env := valid()
	env.Destination = &controlv1.Destination{Host: host}
	got := contract.Validate(env)
	ours := contract.CheckHost(host)
	want := len(host) <= contract.MaxStringBytes && contract.CheckIdentifier(host) == nil && ours == nil
	if (got == nil) != want {
		t.Fatalf("Validate accepts host %+q: %v; the bound, the identifier rule and CheckHost accept it: %v (Validate: %v, CheckHost: %v)",
			host, got == nil, want, got, ours)
	}
	if ours == nil || len(host) > contract.MaxStringBytes || contract.CheckIdentifier(host) != nil {
		return
	}
	var theirs, mine *contract.ValidationError
	if !errors.As(got, &theirs) || !errors.As(ours, &mine) {
		t.Fatalf("host %+q: Validate = %v and CheckHost = %v, not both *ValidationError", host, got, ours)
	}
	if theirs.Field != "destination.host" || mine.Field != "" {
		t.Fatalf("host %+q: Validate names %q, CheckHost names %q; want destination.host and no field", host, theirs.Field, mine.Field)
	}
	if theirs.Err.Error() != mine.Err.Error() {
		t.Fatalf("host %+q: two reasons for one refusal: Validate %q, CheckHost %q", host, theirs.Err, mine.Err)
	}
}

// TestCheckHostRefusalNamesNoFieldAndNoHost: the refusal is a *ValidationError
// with no Field, wrapping ErrInvalidValue, one printable line that names a byte
// by its position and never repeats the host, which is the caller's.
func TestCheckHostRefusalNamesNoFieldAndNoHost(t *testing.T) {
	const marker = "hunter2"
	for _, host := range []string{
		marker + ".EXAMPLE", marker + ".example.", "." + marker, marker + "..example", marker + ":443", "::" + marker,
	} {
		err := contract.CheckHost(host)
		if err == nil {
			t.Fatalf("CheckHost(%q) = nil, so this case proves nothing", host)
		}
		assertRefused(t, err, contract.ErrInvalidValue, "")
		if strings.Contains(err.Error(), marker) {
			t.Errorf("the refusal repeats the caller's host: %v", err)
		}
		for _, r := range err.Error() {
			if !unicode.IsPrint(r) {
				t.Errorf("the refusal %+q holds %U", err.Error(), r)
			}
		}
	}
}
