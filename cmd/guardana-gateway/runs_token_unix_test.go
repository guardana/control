//go:build unix

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/runs"
)

// TestAPathThatIsATokenIsNeverRepeated: a token pasted where the token file's
// path belongs is refused without the path, whichever refusal it meets.
func TestAPathThatIsATokenIsNeverRepeated(t *testing.T) {
	secret := strings.Repeat("Q", 43)
	missing := "run-0123456789abcdef0123456789abcdef." + secret
	wide := writeToken(t, t.TempDir(), "run-0123456789abcdef0123456789abcdef."+secret, "x", 0o644)
	for _, path := range []string{missing, wide} {
		_, err := readRunToken(path)
		if err == nil {
			t.Fatalf("%d characters: read as a token file", len(path))
		}
		if strings.Contains(err.Error(), secret[:12]) {
			t.Errorf("the refusal repeats the path: %v", err)
		}
	}
}

// TestTheDirectorysCausesAreTheListenersCauses: the plane hands a refusal's
// cause from the runs directory to the listener by its spelling, so each cause
// the directory names is one the listener counts under its own label.
func TestTheDirectorysCausesAreTheListenersCauses(t *testing.T) {
	var got []gateway.RunCause
	for _, c := range runs.Causes() {
		got = append(got, gateway.RunCause(c))
	}
	if !slices.Equal(got, gateway.RunCauses()) {
		t.Errorf("the directory's causes %v, the listener's %v", got, gateway.RunCauses())
	}
}
