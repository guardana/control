package secretscan_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

const (
	afterJSON           = "raw after JSON-unescaping"
	afterJSONAndPercent = "raw after JSON-unescaping and percent-decoding"
	afterPercentAndJSON = "raw after percent-decoding and JSON-unescaping"
)

func oneSecretSet(t *testing.T, value string) *secretscan.Set {
	t.Helper()
	set, err := secretscan.New([]secretscan.Secret{{Key: "k", Value: value, Kind: secretscan.Credential}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return set
}

// Every text escapes some characters and leaves others of the same kind as
// written, which no precomputed spelling holds.
func TestMixedJSONEscapesAreFound(t *testing.T) {
	for _, tc := range []struct{ name, value, text, spelling string }{
		{"html-safe lt only", "ab<cd&ef", `ab\u003ccd&ef`, afterJSON},
		{"html-safe amp only", "ab<cd&ef", `ab<cd\u0026ef`, afterJSON},
		{"upper-case hex", "ab<cd&ef", `ab\u003Ccd&ef`, afterJSON},
		{"plain letter escaped", "ab<cd&ef", `\u0061b<cd&ef`, afterJSON},
		{"one slash escaped", "a/b/c-secret", `a\/b/c-secret`, afterJSON},
		{"one non-ASCII escaped", "é😀-token", `é\uD83D\uDE00-token`, afterJSON},
		{"lone high surrogate", "a\uFFFDb-secret", `a\ud800b-secret`, afterJSON},
		{"lone low surrogate", "a\uFFFDb-secret", `a\udc00b-secret`, afterJSON},
		{"high before a non-surrogate", "x\uFFFDA-secret", `x\ud83d\u0041-secret`, afterJSON},
		{"two highs", "x\uFFFD😀-secret", `x\ud83d\ud83d\ude00-secret`, afterJSON},
		{"malformed escapes kept", `\xb\u12G<-secret-\`, `\xb\u12G\u003c-secret-\`, afterJSON},
		{"escaped backslash before u", `a\u003c<&-secret`, `a\\u003c\u003c&-secret`, afterJSON},
		{"escaped twice", "a<b-secret", `a\\u003cb-secret`, "json-escaped after JSON-unescaping"},
		{"json then percent", "ab<cd&ef", `ab%3Ccd\u0026ef`, afterJSONAndPercent},
		{"json then percent with plus", "ab<cd ef&1", `ab\u003ccd+ef&1`, afterJSONAndPercent},
		{"percent then json", "ab<cd&ef", `ab%5Cu003ccd&ef`, afterPercentAndJSON},
		{"percent then json lower hex", "ab<cd&ef", `ab%5cu003ccd&ef`, afterPercentAndJSON},
		{"percent then json with plus", "ab<cd ef&1", `ab%5Cu003ccd+ef&1`, afterPercentAndJSON},
		{"escaped u after percent", "ab<cd&ef", `ab\%75003ccd&ef`, afterPercentAndJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := oneSecretSet(t, tc.value)
			checkFoundKey(t, set.ScanText("said: "+tc.text+" end"), "k")
			if got := set.ScanText("said: " + tc.text + " end"); got.Spelling != tc.spelling {
				t.Fatalf("spelling %q, want %q", got.Spelling, tc.spelling)
			}
		})
	}
}

func TestEverySimpleJSONEscapeIsDecoded(t *testing.T) {
	for _, tc := range []struct{ escape, char string }{
		{`\"`, `"`}, {`\\`, `\`}, {`\/`, `/`}, {`\b`, "\b"},
		{`\f`, "\f"}, {`\n`, "\n"}, {`\r`, "\r"}, {`\t`, "\t"},
	} {
		t.Run(tc.escape, func(t *testing.T) {
			set := oneSecretSet(t, "k"+tc.char+"-secret<&")
			got := set.ScanText("k" + tc.escape + `-secret\u003c&`)
			if got != (secretscan.Verdict{State: secretscan.Found, Key: "k", Spelling: afterJSON}) {
				t.Fatalf("verdict %+v, want found %q", got, afterJSON)
			}
		})
	}
}

func TestMixedJSONEscapesInAJSONDocument(t *testing.T) {
	set := oneSecretSet(t, "ab<cd&ef")
	for _, doc := range []string{
		`{"t":"{\"h\":\"ab\\u003ccd&ef\"}"}`,
		`{"ab\\u003ccd&ef":1}`,
		`["ab\\u0026x", "ab\\u003ccd&ef"]`,
	} {
		t.Run(doc, func(t *testing.T) {
			checkFound(t, set.ScanJSON([]byte(doc)), "k", afterJSON)
		})
	}
}

func TestBase64WithOneEscapedSlashIsFound(t *testing.T) {
	set := oneSecretSet(t, slashyValue)
	encoded := blob(base64.StdEncoding, 0, slashyValue)
	escaped := strings.Replace(encoded, "/", `\/`, 1)
	if escaped == encoded || strings.Count(encoded, "/") < 2 {
		t.Fatalf("fixture %q does not mix escaped and plain slashes", encoded)
	}
	checkFound(t, set.ScanText(escaped), "k", "base64 after JSON-unescaping")
}

func TestJSONUnescapingDecodesOnlyWellFormedEscapes(t *testing.T) {
	set := oneSecretSet(t, "a<b-secret")
	for _, text := range []string{
		`a\u003 b-secret`, `a\x3cb-secret`, `a\U003cb-secret`, `a\u003gb-secret`,
	} {
		if got := set.ScanText(text); got.State != secretscan.Clean {
			t.Fatalf("%s: verdict %+v, want clean", text, got)
		}
	}
}

// TestALowerCasePercentEncodedBackslashStartsAnEscape: a JSON escape whose
// backslash is percent-encoded in lower case is found once both are undone.
func TestALowerCasePercentEncodedBackslashStartsAnEscape(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{{Key: "k", Value: "secretAB", Kind: secretscan.Credential}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"secret%5cu0041B", "secret%5Cu0041B"} {
		if v := set.ScanText(text); v.State != secretscan.Found {
			t.Errorf("%q: %+v, want Found", text, v)
		}
	}
}
