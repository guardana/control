package policykey_test

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

func TestKeyIDAndPublicLineGoldens(t *testing.T) {
	pub := ed25519.PublicKey(mustHex(t, rfcPublicHex))
	if got := policykey.KeyID(pub); got != rfcKeyID {
		t.Errorf("KeyID = %q, want %q", got, rfcKeyID)
	}
	if got := policykey.FormatPublic(pub); got != rfcPublicLine {
		t.Errorf("FormatPublic = %q, want %q", got, rfcPublicLine)
	}
	if derived, _ := rfcKey(t).Public().(ed25519.PublicKey); policykey.KeyID(derived) != rfcKeyID {
		t.Errorf("the key built from the RFC's seed has id %q, want %q", policykey.KeyID(derived), rfcKeyID)
	}
	want := "key_id: " + rfcKeyID + "\npublic_key: " + rfcPublicLine + "\n"
	if got := policykey.ConfigLines(pub); got != want {
		t.Errorf("ConfigLines = %q, want %q", got, want)
	}
	if got := policykey.KeyID(pub[:31]); got != "" {
		t.Errorf("KeyID of 31 bytes = %q, want no id", got)
	}
}

// TestParsePublicTakesOneSpelling: the standard decoder accepts line breaks,
// and padding bits that are not zero; each spelling below decodes to the RFC's
// key or to one byte off it, and only the line itself, or the line with the
// one newline the public key file ends in, may be read.
func TestParsePublicTakesOneSpelling(t *testing.T) {
	for _, line := range []string{rfcPublicLine, rfcPublicLine + "\n"} {
		key, err := policykey.ParsePublic(line)
		if err != nil || string(key) != string(mustHex(t, rfcPublicHex)) {
			t.Errorf("ParsePublic(%q) = %x, %v", line, key, err)
		}
	}
	for name, line := range map[string]string{
		"two trailing newlines": rfcPublicLine + "\n\n",
		"a trailing CRLF":       rfcPublicLine + "\r\n",
		"a trailing CR":         rfcPublicLine + "\r",
		"a trailing space":      rfcPublicLine + " ",
		"a space, then newline": rfcPublicLine + " \n",
		"a newline alone":       "\n",
		"a leading newline":     "\n" + rfcPublicLine,
		"a line break inside":   rfcPublicLine[:20] + "\n" + rfcPublicLine[20:],
		"a carriage return":     rfcPublicLine[:20] + "\r" + rfcPublicLine[20:],
		"padding bits set":      strings.TrimSuffix(rfcPublicLine, "o=") + "p=",
		"no padding":            strings.TrimSuffix(rfcPublicLine, "="),
		"the URL-safe alphabet": strings.ReplaceAll(rfcPublicLine, "/", "_"),
		"31 bytes":              strings.Repeat("A", 40) + "AA==",
		"33 bytes":              strings.Repeat("A", 44),
		"leading space":         " " + rfcPublicLine,
		"empty":                 "",
	} {
		if _, err := policykey.ParsePublic(line); !errors.Is(err, policykey.ErrPublicLine) {
			t.Errorf("%s: ParsePublic(%q) = %v, want ErrPublicLine", name, line, err)
		}
	}
}
