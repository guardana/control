package main

import (
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

// TestTrailWithholdsKeyTextInAChainReason: the chain check's reason quotes
// the id a broken link names, and the line trail prints withholds it.
func TestTrailWithholdsKeyTextInAChainReason(t *testing.T) {
	_, body := fixtureKeyText(t)
	broken := trailOf("a", kProposed, kDecided, kBlocked)
	broken[1].PrevEventId = body
	status, stdout, stderr := trailCommand(t, trailFile(t, "", broken...))
	if status != exitFail || !strings.Contains(stdout, "links to") || !strings.Contains(stdout, policykey.KeyTextWithheld) {
		t.Errorf("a broken link to a body line: %d %q %q", status, stdout, stderr)
	}
	if holdsKeyWindow(stdout+stderr, body) {
		t.Errorf("trail printed key text from a chain reason: %q %q", stdout, stderr)
	}
}

// TestTrailWithholdsKeyTextInAnID: a request id holding a body line is
// withheld when trail quotes it and when it cuts one past maxShownID.
func TestTrailWithholdsKeyTextInAnID(t *testing.T) {
	_, body := fixtureKeyText(t)
	for name, c := range map[string]struct {
		id  string
		cut bool
	}{
		"quoted":          {"=" + body[:40], false},
		"cut":             {strings.Repeat("=", 10) + body, true},
		"cut, a tail run": {body + "-tail", true},
	} {
		status, stdout, stderr := trailCommand(t, trailFile(t, "", trailOf(c.id, kProposed, kDecided, kBlocked)...))
		if status != exitOK || !strings.Contains(stdout, `request="`+policykey.KeyTextWithheld) {
			t.Errorf("%s id: %d %q %q", name, status, stdout, stderr)
		}
		if strings.Contains(stdout, "(cut)") != c.cut {
			t.Errorf("%s id: cut is %v, want %v: %q", name, !c.cut, c.cut, stdout)
		}
		if holdsKeyWindow(stdout+stderr, body) {
			t.Errorf("%s id: trail printed key text: %q %q", name, stdout, stderr)
		}
	}
}
