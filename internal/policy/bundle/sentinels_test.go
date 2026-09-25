package bundle_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
)

// Each line stops compiling if its sentinel becomes a variable again. Any code
// in the binary could set a variable to nil, and VerifyBytes returns its
// refusals bare, so nil would read as a verified signature.
const (
	_ = bundle.ErrKeySize
	_ = bundle.ErrKeyPair
	_ = bundle.ErrUnknownKey
	_ = bundle.ErrWeakKey
	_ = bundle.ErrSignature
)

type sentinel struct {
	name string
	err  error
}

// sentinels lists every refusal the package exports.
func sentinels() []sentinel {
	return []sentinel{
		{"ErrKeySize", bundle.ErrKeySize},
		{"ErrKeyPair", bundle.ErrKeyPair},
		{"ErrUnknownKey", bundle.ErrUnknownKey},
		{"ErrWeakKey", bundle.ErrWeakKey},
		{"ErrSignature", bundle.ErrSignature},
	}
}

// A caller maps each refusal to its own check with errors.Is, so no sentinel
// may match another, by being equal to it or by wrapping it.
func TestSentinelsArePairwiseDistinct(t *testing.T) {
	if overlap := sentinelOverlap(); overlap != "" {
		t.Fatal(overlap)
	}
}

// sentinelOverlap names the first sentinel that matches another under
// errors.Is, or returns "".
func sentinelOverlap() string {
	all := sentinels()
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a.err, b.err) {
				return fmt.Sprintf("errors.Is(%s, %s) = true", a.name, b.name)
			}
		}
	}
	return ""
}

// reporter is what a test, a rapid property and the fuzz target have in
// common.
type reporter interface {
	Errorf(format string, args ...any)
}

// checkOnly reports a failure unless err matches the sentinel want under
// errors.Is and none of the other four; a nil want asks for a nil err. It
// returns whether err passed.
func checkOnly(t reporter, err, want error, format string, args ...any) bool {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	what := fmt.Sprintf(format, args...)
	if want == nil {
		if err != nil {
			t.Errorf("%s = %v, want nil", what, err)
			return false
		}
		return true
	}
	if overlap := sentinelOverlap(); overlap != "" {
		t.Errorf("%s = %v, and the sentinels overlap: %s", what, err, overlap)
		return false
	}
	ok, named := true, 0
	for _, s := range sentinels() {
		// The five are distinct, so want matches s only if want is s.
		isWant := errors.Is(want, s.err)
		if isWant {
			named++
		}
		if got := errors.Is(err, s.err); got != isWant {
			t.Errorf("%s = %v; errors.Is(err, %s) = %t, want %t", what, err, s.name, got, isWant)
			ok = false
		}
	}
	if named != 1 {
		t.Errorf("%s: want %v is not one of the five sentinels", what, want)
		return false
	}
	return ok
}
