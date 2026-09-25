package keytext_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/guardana/control/internal/keytext"
)

// keyFile encodes the key of seed as a PKCS#8 PEM file with the standard
// library's own encoders, not with any code of this module.
func keyFile(t testing.TB, seed []byte) (file, body string) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	file = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return file, strings.Split(file, "\n")[1]
}

// keyTexts is a key file of a fixed seed, its body line, and the part of that
// line every Ed25519 key file begins with.
func keyTexts(t testing.TB) (file, body, start string) {
	t.Helper()
	seed := sha256.Sum256([]byte("fixture"))
	file, body = keyFile(t, seed[:])
	return file, body, body[:21]
}

// TestThePrefixIsWhatTheStandardLibraryWrites: every PKCS#8 Ed25519 key the
// standard library encodes begins with PKCS8Prefix and then the seed.
func TestThePrefixIsWhatTheStandardLibraryWrites(t *testing.T) {
	for i := range 4 {
		seed := sha256.Sum256([]byte{byte(i)})
		der, err := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed(seed[:]))
		if err != nil {
			t.Fatal(err)
		}
		if string(der) != keytext.PKCS8Prefix+string(seed[:]) {
			t.Errorf("key %d encodes as %x, want the prefix and then the seed", i, der)
		}
	}
}

// TestWithheldIsItsOwnPhrase pins the stand-in, since every other test names
// it through the constant.
func TestWithheldIsItsOwnPhrase(t *testing.T) {
	if keytext.Withheld != "[key text withheld]" {
		t.Errorf("Withheld = %q", keytext.Withheld)
	}
}

// TestPrintableWithholdsKeyTextAndKeepsTheRest: each shape of key text is
// replaced, and the text around it stays as it was.
func TestPrintableWithholdsKeyTextAndKeepsTheRest(t *testing.T) {
	file, body, start := keyTexts(t)
	block := strings.TrimSpace(file)
	const w = keytext.Withheld
	for name, c := range map[string]struct{ in, want string }{
		"a body line alone":                     {body, w},
		"a body line inside a message":          {"open " + body + ": no such file", "open " + w + ": no such file"},
		"base64 on both sides of the start":     {"value Zm9v" + body + "Zm9v= left", "value " + w + " left"},
		"two body lines":                        {body + " and " + body, w + " and " + w},
		"a PEM block inside a message":          {"key " + block + " here", "key " + w + " here"},
		"a PEM file with its newline":           {file, strconv.Quote(w + "\n")},
		"an opening marker with no closing one": {"x -----BEGIN CERTIFICATE----- rest", "x " + w},
		"a closing marker alone":                {"tail -----END CERTIFICATE----- after", "tail " + w + " after"},
		"a closing marker with no dashes after": {"tail -----END CERTIFICATE", "tail " + w},
		"a PEM block, then a body line":         {block + " " + body + " end", w + " " + w + " end"},
		"the start one character short":         {start[:20] + " ok", start[:20] + " ok"},
		"five dashes that open nothing":         {"-----x-----", "-----x-----"},
		"nothing":                               {"", ""},
	} {
		if got := keytext.Printable(c.in); got != c.want {
			t.Errorf("%s: Printable(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// TestPrintableQuotesWhatBreaksALine: a control character, a line or
// paragraph separator, a format character and bytes that are not UTF-8 each
// quote the message, whole; plain text in any script is left bare.
func TestPrintableQuotesWhatBreaksALine(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"a line break":             {"a\nb", `"a\nb"`},
		"a C1 control":             {"a\u0085b", `"a\u0085b"`},
		"a line separator":         {"a\u2028b", `"a\u2028b"`},
		"a paragraph separator":    {"a\u2029b", `"a\u2029b"`},
		"a zero width space":       {"a\u200bb", `"a\u200bb"`},
		"a right to left override": {"a\u202eb", `"a\u202eb"`},
		"bytes that are not UTF-8": {"a\xffb", `"a\xffb"`},
		"plain non-ASCII text":     {"reguła „żółw” — ok", "reguła „żółw” — ok"},
		"markup stays bare":        {`<img src=x onerror=alert(1)>`, `<img src=x onerror=alert(1)>`},
	} {
		if got := keytext.Printable(c.in); got != c.want {
			t.Errorf("%s: Printable(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// TestHolds: each marker and the start of a body line are key text, and a
// start one character short is not.
func TestHolds(t *testing.T) {
	_, body, start := keyTexts(t)
	for _, s := range []string{"-----BEGIN", "x-----END", start, "/tmp/" + body} {
		if !keytext.Holds(s) {
			t.Errorf("Holds(%q) = false", s)
		}
	}
	for _, s := range []string{start[:20], "-----", "/etc/plane/policy.bundle", ""} {
		if keytext.Holds(s) {
			t.Errorf("Holds(%q) = true", s)
		}
	}
}

// TestPrintableWithholdsAURLSafeBodyLine: a body line spelled in the URL-safe
// alphabet shares its start with the standard one, and the whole run is
// withheld, whatever seed characters become - or _.
func TestPrintableWithholdsAURLSafeBodyLine(t *testing.T) {
	const keys = 1000
	withDash, withUnderscore := 0, 0
	for i := range keys {
		seed := sha256.Sum256([]byte{byte(i), byte(i >> 8)})
		_, body := keyFile(t, seed[:])
		der, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			t.Fatal(err)
		}
		url := base64.URLEncoding.EncodeToString(der)
		withDash += min(strings.Count(url, "-"), 1)
		withUnderscore += min(strings.Count(url, "_"), 1)
		if got := keytext.Printable("id " + url + ": unknown"); got != "id "+keytext.Withheld+": unknown" {
			t.Fatalf("Printable of a URL-safe body line = %q", got)
		}
	}
	if withDash == 0 || withUnderscore == 0 {
		t.Fatalf("of %d URL-safe body lines, %d hold a - and %d a _: the input tests neither", keys, withDash, withUnderscore)
	}
}

// FuzzPrintable: whatever the input, the output holds no PEM marker, no start
// of a body line, no rune that breaks a line and nothing that is not UTF-8.
func FuzzPrintable(f *testing.F) {
	file, body, _ := keyTexts(f)
	for _, seed := range []string{"", body, file, "a\u2028b", "x-----BEGIN", "-----END" + body, "a\xff" + body} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := keytext.Printable(s)
		if keytext.Holds(got) {
			t.Fatalf("Printable(%q) = %q holds key text", s, got)
		}
		if !utf8.ValidString(got) || strings.IndexFunc(got, func(r rune) bool {
			return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Cf)
		}) >= 0 {
			t.Fatalf("Printable(%q) = %q breaks a line", s, got)
		}
	})
}
