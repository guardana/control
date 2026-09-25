package policykey_test

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"testing/cryptotest"

	"github.com/guardana/control/internal/policykey"
)

// armor spells a PEM block by hand, so a test can write one pem.Encode never
// would. The markers are assembled here rather than written out, so the source
// holds nothing a secret scanner reads as a key.
func armor(blockType, body string) string {
	return "-----BEGIN " + blockType + "-----\n" + body + "\n-----END " + blockType + "-----\n"
}

// TestMarshalPrivateIsRFC8410: the key file is the PKCS#8 encoding RFC 8410
// fixes for an Ed25519 key, the prefix written out as a literal, so a file
// OpenSSL writes and one keygen writes are the same bytes for the same seed.
func TestMarshalPrivateIsRFC8410(t *testing.T) {
	raw, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatalf("MarshalPrivate: %v", err)
	}
	der := mustHex(t, rfc8410Prefix+rfcSeedHex)
	want := armor("PRIVATE KEY", base64.StdEncoding.EncodeToString(der))
	if string(raw) != want {
		t.Errorf("the key file is\n%s\nwant\n%s", raw, want)
	}
	if got := []byte(policykey.PKCS8Prefix); !bytes.Equal(got, mustHex(t, rfc8410Prefix)) {
		t.Errorf("PKCS8Prefix = %x, want %s", got, rfc8410Prefix)
	}
	key, err := policykey.ParsePrivate(raw)
	if err != nil || !key.Equal(rfcKey(t)) {
		t.Errorf("ParsePrivate(MarshalPrivate(key)) = %v, want the same key", err)
	}
	if _, err := policykey.MarshalPrivate(rfcKey(t)[:63]); err == nil {
		t.Error("MarshalPrivate took a key of 63 bytes")
	}
}

// refusalCases are key files ParsePrivate refuses, each with its own
// refusal. They are built at run time from the RFC's key, so none is a key
// file in the tree.
func refusalCases(t *testing.T) map[string]struct {
	raw  string
	want policykey.Error
} {
	t.Helper()
	good, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatalf("MarshalPrivate: %v", err)
	}
	der := mustHex(t, rfc8410Prefix+rfcSeedHex)
	body := base64.StdEncoding.EncodeToString(der)
	block := func(blockType string, payload []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: payload}))
	}
	return map[string]struct {
		raw  string
		want policykey.Error
	}{
		"empty":                     {"", policykey.ErrEmpty},
		"white space alone":         {"\n  \n", policykey.ErrPreamble},
		"a line before the block":   {"key for prod\n" + string(good), policykey.ErrPreamble},
		"a newline before":          {"\n" + string(good), policykey.ErrPreamble},
		"a broken block first":      {armor("PRIVATE KEY", "!!!!") + string(good), policykey.ErrPreamble},
		"a marker and nothing more": {"-----BEGIN PRIVATE KEY-----\n" + body + "\n", policykey.ErrNotPEM},
		"headers": {
			string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Comment": "prod"}, Bytes: der})),
			policykey.ErrHeaders,
		},
		"a second block":             {string(good) + string(good), policykey.ErrTrailing},
		"a word after":               {string(good) + "done", policykey.ErrTrailing},
		"a form feed after":          {string(good) + "\f", policykey.ErrTrailing},
		"a vertical tab after":       {string(good) + "\n\v", policykey.ErrTrailing},
		"a no-break space after":     {string(good) + "\u00a0\n", policykey.ErrTrailing},
		"a next-line mark after":     {string(good) + "\u0085", policykey.ErrTrailing},
		"a PKCS#1 RSA public key":    {block("RSA PUBLIC KEY", der), policykey.ErrPublic},
		"an OpenSSH key":             {block("OPENSSH PRIVATE KEY", []byte("openssh-key-v1\x00")), policykey.ErrOpenSSH},
		"an encrypted key":           {block("ENCRYPTED PRIVATE KEY", der), policykey.ErrEncrypted},
		"a SEC 1 EC key":             {block("EC PRIVATE KEY", der), policykey.ErrEC},
		"a PKCS#1 RSA key":           {block("RSA PRIVATE KEY", der), policykey.ErrRSA},
		"a public key":               {block("PUBLIC KEY", der[16:]), policykey.ErrPublic},
		"a certificate":              {block("CERTIFICATE", der), policykey.ErrBlockType},
		"a lower-case type":          {block("private key", der), policykey.ErrBlockType},
		"not DER":                    {block("PRIVATE KEY", []byte("not der at all")), policykey.ErrPKCS8},
		"DER with a trailing byte":   {block("PRIVATE KEY", append(bytes.Clone(der), 0)), policykey.ErrPKCS8},
		"a PKCS#8 ECDSA key":         {block("PRIVATE KEY", pkcs8(t, ecdsaKey(t))), policykey.ErrEC},
		"a PKCS#8 RSA key":           {block("PRIVATE KEY", pkcs8(t, rsaKey(t))), policykey.ErrRSA},
		"a PKCS#8 X25519 key":        {block("PRIVATE KEY", pkcs8(t, x25519Key(t))), policykey.ErrKeyType},
		"the seed in base64 instead": {base64.StdEncoding.EncodeToString(der[16:]) + "\n", policykey.ErrPreamble},
	}
}

func pkcs8(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding a %T: %v", key, err)
	}
	return der
}

func ecdsaKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("building an ECDSA key: %v", err)
	}
	return key
}

// rsaKey draws from a seeded random source: RSA has no way to build a key
// from a seed directly.
func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	cryptotest.SetGlobalRandom(t, 7)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("building an RSA key: %v", err)
	}
	return key
}

func x25519Key(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	key, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("building an X25519 key: %v", err)
	}
	return key
}

// TestParsePrivateRefusals: each file is refused with its own constant,
// returned bare, so the text can hold nothing of the file.
func TestParsePrivateRefusals(t *testing.T) {
	for name, c := range refusalCases(t) {
		key, err := policykey.ParsePrivate([]byte(c.raw))
		if key != nil || err == nil {
			t.Errorf("%s: ParsePrivate accepted the file", name)
			continue
		}
		if got, ok := err.(policykey.Error); !ok || got != c.want { //nolint:errorlint // the refusal must be the bare constant, not a wrapper around it
			t.Errorf("%s: ParsePrivate = %v, want exactly %q", name, err, c.want)
		}
	}
}

func TestParsePrivateTakesTrailingWhiteSpace(t *testing.T) {
	good, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"", "\n", "\r\n", " \t\n\n"} {
		key, err := policykey.ParsePrivate(append(bytes.Clone(good), tail...))
		if err != nil || !key.Equal(rfcKey(t)) {
			t.Errorf("a key file ending %q: %v", tail, err)
		}
	}
}

// TestRefusalsAreConstants: every refusal case's text is a constant of this
// package, and none of the texts carries a line of the key's own PEM body.
func TestRefusalsAreConstants(t *testing.T) {
	body := base64.StdEncoding.EncodeToString(mustHex(t, rfc8410Prefix+rfcSeedHex))
	for name, c := range refusalCases(t) {
		_, err := policykey.ParsePrivate([]byte(c.raw))
		if err == nil || strings.Contains(err.Error(), body[:16]) || !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
