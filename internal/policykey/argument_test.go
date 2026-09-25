package policykey_test

import (
	"crypto/ed25519"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

// TestCheckArgumentsRefusesKeyText: each kind of key text, alone and as a
// later argument after a plain one, is refused; the start of a body line one
// character short, a plain path and non-ASCII text pass.
func TestCheckArgumentsRefusesKeyText(t *testing.T) {
	refused := map[string]string{
		"a PEM marker":                      "-----BEGIN PRIVATE KEY-----",
		"a closing PEM marker":              "-----END PRIVATE KEY-----",
		"the start of a body line":          "MC4CAQAwBQYDK2VwBCIEI",
		"a line break":                      "policy\nbundle",
		"a carriage return":                 "policy\rbundle",
		"a tab":                             "policy\tbundle",
		"a NUL":                             "policy\x00bundle",
		"a delete":                          "policy\x7fbundle",
		"a C1 control":                      "policy\u009bbundle",
		"a next line":                       "policy\u0085bundle",
		"a PEM marker inside a longer path": "/tmp/-----BEGIN/x",
	}
	for i, key := range []ed25519.PrivateKey{rfcKey(t), otherKey()} {
		pemFile, err := policykey.MarshalPrivate(key)
		if err != nil {
			t.Fatal(err)
		}
		refused["the body line of key "+strconv.Itoa(i)] = strings.Split(string(pemFile), "\n")[1]
		refused["a PEM file, trimmed, of key "+strconv.Itoa(i)] = strings.TrimSpace(string(pemFile))
	}
	for name, a := range refused {
		for _, args := range [][]string{{a}, {"--out", a}, {"plain", "also-plain", a}} {
			if err := policykey.CheckArguments(args); !errors.Is(err, policykey.ErrKeyText) {
				t.Errorf("%s at argument %d: CheckArguments = %v, want ErrKeyText", name, len(args)-1, err)
			}
		}
	}
	for name, a := range map[string]string{
		"a body line's start one character short": "MC4CAQAwBQYDK2VwBCIE",
		"a plain path":   "/etc/plane/policy.bundle",
		"non-ASCII text": "raport-żółw.json",
		"a space":        "checked with the desk",
		"nothing":        "",
	} {
		if err := policykey.CheckArguments([]string{"--out", a}); err != nil {
			t.Errorf("%s: CheckArguments = %v, want nil", name, err)
		}
	}
	if err := policykey.CheckArguments(nil); err != nil {
		t.Errorf("no argument: CheckArguments = %v, want nil", err)
	}
}

// TestErrKeyTextRepeatsNoKeyText: the refusal's own text holds none of what
// it refuses, so printing it repeats nothing.
func TestErrKeyTextRepeatsNoKeyText(t *testing.T) {
	if err := policykey.CheckArguments([]string{policykey.ErrKeyText.Error()}); err != nil {
		t.Errorf("the refusal's text is itself refused: %v", err)
	}
}
