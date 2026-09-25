package ioprobe

import (
	"net/http"
	_ "net/netip" //nolint:depguard // the exception the gate refuses
	"testing"
)

// A test file reaches the network. depguard is the only mechanism that reads
// test files, so this refusal is the one the probe expects from it alone. The
// second import carries an inline exception, which the gate refuses as well.
func TestDependencyProbe(t *testing.T) {
	if http.MethodGet == "" {
		t.Fatal("unreachable")
	}
}
