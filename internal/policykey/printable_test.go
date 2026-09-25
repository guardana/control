package policykey_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/guardana/control/internal/policykey"
)

// keyTexts is a key file of the fixed key, its body line, and the start of
// that line that keygen writes for every key.
func keyTexts(t testing.TB) (pemFile, body, start string) {
	t.Helper()
	raw, err := policykey.MarshalPrivate(otherKey())
	if err != nil {
		t.Fatal(err)
	}
	body = strings.Split(string(raw), "\n")[1]
	return string(raw), body, body[:21]
}

// TestKeyTextWithheldIsItsOwnPhrase pins the stand-in, since every other test
// names it through the constant.
func TestKeyTextWithheldIsItsOwnPhrase(t *testing.T) {
	if policykey.KeyTextWithheld != "[key text withheld]" {
		t.Errorf("KeyTextWithheld = %q", policykey.KeyTextWithheld)
	}
}

// TestPrintableWithholdsKeyTextAndKeepsTheRest: each shape of key text is
// replaced, and the text around it stays as it was.
func TestPrintableWithholdsKeyTextAndKeepsTheRest(t *testing.T) {
	pemFile, body, start := keyTexts(t)
	pemBlock := strings.TrimSpace(pemFile)
	const w = policykey.KeyTextWithheld
	for name, c := range map[string]struct{ in, want string }{
		"a body line alone":                     {body, w},
		"a body line inside a message":          {"open " + body + ": no such file", "open " + w + ": no such file"},
		"base64 on both sides of the start":     {"value Zm9v" + body + "Zm9v= left", "value " + w + " left"},
		"two body lines":                        {body + " and " + body, w + " and " + w},
		"a PEM block inside a message":          {"key " + pemBlock + " here", "key " + w + " here"},
		"a PEM file with its newline":           {pemFile, strconv.Quote(w + "\n")},
		"a PEM block, quoted":                   {"read " + strconv.Quote(pemFile) + " then", "read " + `"` + w + `\n"` + " then"},
		"an opening marker with no closing one": {"x -----BEGIN CERTIFICATE----- rest", "x " + w},
		"a closing marker alone":                {"tail -----END CERTIFICATE----- after", "tail " + w + " after"},
		"a closing marker with no dashes after": {"tail -----END CERTIFICATE", "tail " + w},
		"a PEM block, then a body line":         {pemBlock + " " + body + " end", w + " " + w + " end"},
		"a body line, then a PEM block":         {body + " " + pemBlock + " end", w + " " + w + " end"},
		"the start one character short":         {start[:20] + " ok", start[:20] + " ok"},
		"five dashes that open nothing":         {"-----x-----", "-----x-----"},
	} {
		if got := policykey.Printable(c.in); got != c.want {
			t.Errorf("%s: Printable(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// TestPrintableQuotesWhatBreaksALine: a control character, a line or
// paragraph separator, a format character and bytes that are not UTF-8 each
// quote the message, whole; key text is withheld before it is quoted.
func TestPrintableQuotesWhatBreaksALine(t *testing.T) {
	_, body, _ := keyTexts(t)
	for name, c := range map[string]struct{ in, want string }{
		"a line break":              {"a\nb", `"a\nb"`},
		"a C1 control":              {"a\u0085b", `"a\u0085b"`},
		"a line separator":          {"a\u2028b", `"a\u2028b"`},
		"a paragraph separator":     {"a\u2029b", `"a\u2029b"`},
		"a zero width space":        {"a\u200bb", `"a\u200bb"`},
		"a right to left override":  {"a\u202eb", `"a\u202eb"`},
		"bytes that are not UTF-8":  {"a\xffb", `"a\xffb"`},
		"key text and a line break": {body + "\nok  forged", `"` + policykey.KeyTextWithheld + `\nok  forged"`},
		"plain non-ASCII text":      {"reguła „żółw” — ok", "reguła „żółw” — ok"},
		"quotes stay bare":          {`"rules[0]": empty`, `"rules[0]": empty`},
	} {
		if got := policykey.Printable(c.in); got != c.want {
			t.Errorf("%s: Printable(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// TestHoldsKeyText: each marker and the start of a body line are key text,
// and a start one character short is not.
func TestHoldsKeyText(t *testing.T) {
	_, body, start := keyTexts(t)
	for _, s := range []string{"-----BEGIN", "x-----END", start, "/tmp/" + body} {
		if !policykey.HoldsKeyText(s) {
			t.Errorf("HoldsKeyText(%q) = false", s)
		}
	}
	for _, s := range []string{start[:20], "-----", "/etc/plane/policy.bundle", ""} {
		if policykey.HoldsKeyText(s) {
			t.Errorf("HoldsKeyText(%q) = true", s)
		}
	}
}

// TestPrintableWithholdsAURLSafeBodyLine: a key file's body line spelled in
// the URL-safe alphabet shares its start with the standard one, and the whole
// run is withheld, whatever seed characters become - or _.
func TestPrintableWithholdsAURLSafeBodyLine(t *testing.T) {
	const keys = 1000
	withDash, withUnderscore := 0, 0
	for i := range keys {
		seed := sha256.Sum256([]byte{byte(i), byte(i >> 8)})
		raw, err := policykey.MarshalPrivate(ed25519.NewKeyFromSeed(seed[:]))
		if err != nil {
			t.Fatal(err)
		}
		der, err := base64.StdEncoding.DecodeString(strings.Split(string(raw), "\n")[1])
		if err != nil {
			t.Fatal(err)
		}
		url := base64.URLEncoding.EncodeToString(der)
		withDash += min(strings.Count(url, "-"), 1)
		withUnderscore += min(strings.Count(url, "_"), 1)
		for _, c := range []struct{ in, want string }{
			{url, policykey.KeyTextWithheld},
			{"id " + url + ": unknown", "id " + policykey.KeyTextWithheld + ": unknown"},
		} {
			if got := policykey.Printable(c.in); got != c.want {
				t.Fatalf("Printable(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	}
	if withDash == 0 || withUnderscore == 0 {
		t.Fatalf("of %d URL-safe body lines, %d hold a - and %d a _: the input tests neither", keys, withDash, withUnderscore)
	}
}

// FuzzPrintable: whatever the input, the output holds no PEM marker, no start
// of a body line, no rune that breaks a line and nothing that is not UTF-8.
func FuzzPrintable(f *testing.F) {
	pemFile, body, _ := keyTexts(f)
	for _, seed := range []string{"", body, pemFile, "a\u2028b", "x-----BEGIN", "-----END" + body, "a\xff" + body} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := policykey.Printable(s)
		if policykey.HoldsKeyText(got) {
			t.Fatalf("Printable(%q) = %q holds key text", s, got)
		}
		if !utf8.ValidString(got) || strings.IndexFunc(got, func(r rune) bool {
			return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Cf)
		}) >= 0 {
			t.Fatalf("Printable(%q) = %q breaks a line", s, got)
		}
	})
}
