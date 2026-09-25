package policykey_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

// refusals is every constant ParsePrivate may return.
var refusals = []policykey.Error{
	policykey.ErrEmpty, policykey.ErrPreamble, policykey.ErrNotPEM, policykey.ErrOpenSSH,
	policykey.ErrEncrypted, policykey.ErrEC, policykey.ErrRSA, policykey.ErrPublic,
	policykey.ErrBlockType, policykey.ErrHeaders, policykey.ErrTrailing, policykey.ErrPKCS8,
	policykey.ErrKeyType,
}

// FuzzParsePrivate: for any bytes, ParsePrivate returns an Ed25519 key or one
// of its own constants, bare, and never panics. The constant is what keeps a
// refusal from echoing the file; the window check states that property on its
// own, skipping the windows every refusal shares with some constant's text.
func FuzzParsePrivate(f *testing.F) {
	good, err := policykey.MarshalPrivate(ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte{})
	f.Add(append([]byte("x\n"), good...))
	f.Add(append(append([]byte{}, good...), good...))
	f.Add([]byte(strings.Replace(string(good), "PRIVATE KEY", "OPENSSH PRIVATE KEY", 2)))
	f.Add([]byte(strings.Replace(string(good), "-----\n", "-----\nComment: x\n\n", 1)))
	var texts strings.Builder
	for _, e := range refusals {
		texts.WriteString(string(e) + "\n")
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		key, err := policykey.ParsePrivate(raw)
		if err == nil {
			if len(key) != ed25519.PrivateKeySize {
				t.Fatalf("accepted a key of %d bytes", len(key))
			}
			return
		}
		got, ok := err.(policykey.Error) //nolint:errorlint // the refusal must be the bare constant
		if !ok || !isRefusal(got) {
			t.Fatalf("the refusal %q is not one of the constants", err)
		}
		msg := err.Error()
		for i := 0; i+8 <= len(raw); i++ {
			w := string(raw[i : i+8])
			if strings.Contains(msg, w) && !strings.Contains(texts.String(), w) {
				t.Fatalf("the refusal %q echoes %q from the input", msg, w)
			}
		}
	})
}

func isRefusal(e policykey.Error) bool {
	for _, r := range refusals {
		if e == r {
			return true
		}
	}
	return false
}
