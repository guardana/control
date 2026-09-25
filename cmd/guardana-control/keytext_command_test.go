package main

import (
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/policykey"
)

// TestKeygenAndSignRefuseKeyTextPastTheDispatcher: each command, called
// without the dispatcher before it, refuses key text as its own argument
// with the dispatcher's line and status, writes nothing and repeats nothing.
func TestKeygenAndSignRefuseKeyTextPastTheDispatcher(t *testing.T) {
	pemFile, err := policykey.MarshalPrivate(ed25519.NewKeyFromSeed(rfcSeed(t)))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Split(string(pemFile), "\n")[1]
	dir := t.TempDir()
	doc := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(doc, []byte(signDocument), 0o600); err != nil {
		t.Fatal(err)
	}
	keyOut := filepath.Join(dir, body)
	bundleOut := filepath.Join(dir, "policy.bundle")
	checks := []struct {
		label   string
		run     func() (int, string, string)
		written string
	}{
		{keygenName, func() (int, string, string) { return invokeCommand(t, keygenCommand, "--out", keyOut) }, keyOut},
		{signName, func() (int, string, string) {
			return invokeCommand(t, signCommand, "--key", body, "--out", bundleOut, doc)
		}, bundleOut},
	}
	for _, c := range checks {
		code, stdout, stderr := c.run()
		want := brand.CLI + ": " + c.label + ": " + policykey.ErrKeyText.Error() + "\n"
		if code != exitUsage || stdout != "" || stderr != want {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d and %q", c.label, code, stdout, stderr, exitUsage, want)
		}
		if strings.Contains(stdout+stderr, body[:21]) {
			t.Errorf("%s: an output repeats the body line", c.label)
		}
		if _, err := os.Lstat(c.written); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %s was written: %v", c.label, c.written, err)
		}
	}
}
