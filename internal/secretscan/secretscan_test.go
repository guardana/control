package secretscan_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

const (
	plainValue   = "opensesame-42"
	spacedValue  = "open sesame 42"
	slashyValue  = "pass????????word"
	specialValue = "pw\"x\\y<z&w/é😀"
	numericValue = "12345678901"
)

func fixtureSecrets() []secretscan.Secret {
	return []secretscan.Secret{
		{Key: "files.endpoint.userinfo", Value: plainValue, Kind: secretscan.Credential},
		{Key: "files.endpoint.query.q", Value: spacedValue, Kind: secretscan.MaybeCredential},
		{Key: "pdp.headers.x-one", Value: slashyValue, Kind: secretscan.Credential},
		{Key: "export.headers.x-two", Value: specialValue, Kind: secretscan.Credential},
		{Key: "files.env.PIN", Value: numericValue, Kind: secretscan.MaybeCredential},
	}
}

func fixtureSet(t *testing.T) *secretscan.Set {
	t.Helper()
	set, err := secretscan.New(fixtureSecrets())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return set
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return string(b)
}

// places puts one text where an upstream's answer can carry it.
func places(t *testing.T, text string) map[string]string {
	t.Helper()
	q := jsonString(t, text)
	return map[string]string{
		"string value": `{"content":[{"type":"text","text":` + q + `}],"isError":false}`,
		"object key":   `{"structuredContent":{` + q + `:true}}`,
		"array item":   `[1,"other",[null,` + q + `]]`,
	}
}

func percentAll(s, format string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, format, s[i])
	}
	return b.String()
}

func blob(enc *base64.Encoding, offset int, value string) string {
	return enc.EncodeToString([]byte(strings.Repeat("z", offset) + value + "!tail"))
}

type foundRow struct {
	name     string
	text     string
	key      string
	spelling string
}

// Each escaped form is written by hand; TestEscapedFixturesDecode proves it
// decodes back to the secret.
var escapedForms = []struct{ name, text string }{
	{"html-safe", `pw\"x\\y\u003cz\u0026w/é😀`},
	{"not html-safe", `pw\"x\\y<z&w/é😀`},
	{"slash escaped", `pw\"x\\y<z&w\/é😀`},
	{"html-safe slash escaped", `pw\"x\\y\u003cz\u0026w\/é😀`},
	{"ascii only", `pw\"x\\y<z&w/\u00e9\ud83d\ude00`},
	{"ascii only html-safe slash escaped", `pw\"x\\y\u003cz\u0026w\/\u00e9\ud83d\ude00`},
	{"ascii only upper-case hex", `pw\"x\\y\u003Cz\u0026w/\u00E9\uD83D\uDE00`},
}

func foundRows() []foundRow {
	rows := []foundRow{
		{"credential raw", "denied for opensesame-42 at host", "files.endpoint.userinfo", "raw"},
		{"maybe raw", "q is open sesame 42 here", "files.endpoint.query.q", "raw"},
		{"maybe plus for space", "GET /x?q=open+sesame+42 401", "files.endpoint.query.q", "raw after percent-decoding"},
		{"maybe percent space", "GET /x?q=open%20sesame%2042", "files.endpoint.query.q", "raw after percent-decoding"},
		{"credential percent upper hex", "u=" + percentAll(plainValue, "%%%02X"), "files.endpoint.userinfo", "raw after percent-decoding"},
		{"credential percent lower hex", "u=" + percentAll(plainValue, "%%%02x"), "files.endpoint.userinfo", "raw after percent-decoding"},
		{"special raw", "header was " + specialValue, "export.headers.x-two", "raw"},
		{"numeric in text", "pin 12345678901 rejected", "files.env.PIN", "raw"},
		{
			"base64 percent-encoded",
			url.QueryEscape(blob(base64.StdEncoding, 0, slashyValue)),
			"pdp.headers.x-one", "base64 after percent-decoding",
		},
	}
	for _, f := range escapedForms {
		rows = append(rows, foundRow{
			"json text " + f.name, `{"headers":{"x-two":"` + f.text + `"}}`,
			"export.headers.x-two", "json-escaped",
		})
	}
	encs := []struct {
		name string
		enc  *base64.Encoding
	}{{"std", base64.StdEncoding}, {"url", base64.URLEncoding}}
	for _, e := range encs {
		for off := 0; off < 3; off++ {
			rows = append(rows,
				foundRow{fmt.Sprintf("base64 %s offset %d plain", e.name, off), blob(e.enc, off, plainValue), "files.endpoint.userinfo", "base64"},
				foundRow{fmt.Sprintf("base64 %s offset %d slashy", e.name, off), blob(e.enc, off, slashyValue), "pdp.headers.x-one", "base64"},
			)
		}
	}
	return rows
}

func allValues() []string {
	return []string{plainValue, spacedValue, slashyValue, specialValue, numericValue}
}

func checkFound(t *testing.T, got secretscan.Verdict, key, spelling string) {
	t.Helper()
	want := secretscan.Verdict{State: secretscan.Found, Key: key, Spelling: spelling}
	if got != want {
		t.Fatalf("verdict %+v, want %+v", got, want)
	}
	if !got.Withhold() {
		t.Fatal("a found verdict does not withhold")
	}
	for _, v := range allValues() {
		if strings.Contains(got.Key, v) || strings.Contains(got.Spelling, v) {
			t.Fatalf("verdict %+v carries a secret value", got)
		}
	}
}

func TestScanJSONFindsEverySpellingInEveryPlace(t *testing.T) {
	set := fixtureSet(t)
	for _, r := range foundRows() {
		for place, doc := range places(t, r.text) {
			t.Run(r.name+"/"+place, func(t *testing.T) {
				checkFound(t, set.ScanJSON([]byte(doc)), r.key, r.spelling)
			})
		}
	}
}

func TestScanTextFindsEverySpelling(t *testing.T) {
	set := fixtureSet(t)
	for _, r := range foundRows() {
		t.Run(r.name, func(t *testing.T) {
			checkFound(t, set.ScanText("upstream said: "+r.text+" (end)"), r.key, r.spelling)
		})
	}
}

func TestScanJSONReadsANumbersText(t *testing.T) {
	set := fixtureSet(t)
	for _, doc := range []string{
		`{"n":12345678901}`,
		`{"n":-9912345678901.5e3}`,
		`[true,12345678901]`,
		`12345678901`,
		`{"12345678901":0}`,
	} {
		t.Run(doc, func(t *testing.T) {
			checkFound(t, set.ScanJSON([]byte(doc)), "files.env.PIN", "raw")
		})
	}
}

func TestEscapedFixturesDecode(t *testing.T) {
	for _, f := range escapedForms {
		var got string
		if err := json.Unmarshal([]byte(`"`+f.text+`"`), &got); err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if got != specialValue {
			t.Fatalf("%s decodes to %q, want %q", f.name, got, specialValue)
		}
		if strings.Contains(f.text, specialValue) {
			t.Fatalf("%s holds the raw value, so it would not test an escape", f.name)
		}
	}
}

func TestBase64FixturesNeedTheirAlphabet(t *testing.T) {
	for off := 0; off < 3; off++ {
		std := blob(base64.StdEncoding, off, slashyValue)
		u := blob(base64.URLEncoding, off, slashyValue)
		if !strings.Contains(std, "/") || !strings.ContainsAny(u, "-_") {
			t.Fatalf("offset %d: std %q or url %q does not exercise its alphabet", off, std, u)
		}
		if strings.Contains(std, slashyValue) || strings.Contains(u, slashyValue) {
			t.Fatalf("offset %d: blob holds the raw value", off)
		}
	}
}

func TestScanCleanAnswers(t *testing.T) {
	set := fixtureSet(t)
	for _, doc := range []string{
		`{}`, `[]`, `"text"`, `0`, `null`, `true`,
		`{"content":[{"type":"text","text":"nothing to see, open sesame"}]}`,
		`{"n":1234567890,"pct":"%zz%4","plus":"a+b"}`,
		`{"u":"` + base64.StdEncoding.EncodeToString([]byte("unrelated bytes here")) + `"}`,
	} {
		t.Run(doc, func(t *testing.T) {
			got := set.ScanJSON([]byte(doc))
			if got != (secretscan.Verdict{State: secretscan.Clean}) {
				t.Fatalf("verdict %+v, want clean", got)
			}
			if got.Withhold() {
				t.Fatal("a clean verdict withholds")
			}
		})
	}
	if got := set.ScanText("nothing here"); got.State != secretscan.Clean {
		t.Fatalf("ScanText verdict %+v, want clean", got)
	}
}

func TestFirstSecretInConfigurationOrderWins(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{
		{Key: "second.listed.first", Value: "beta-value-1", Kind: secretscan.Credential},
		{Key: "first.listed.second", Value: "alpha-value-1", Kind: secretscan.Credential},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := set.ScanText("alpha-value-1 and beta-value-1")
	checkFoundKey(t, got, "second.listed.first")
}

func checkFoundKey(t *testing.T, got secretscan.Verdict, key string) {
	t.Helper()
	if got.State != secretscan.Found || got.Key != key {
		t.Fatalf("verdict %+v, want found under %q", got, key)
	}
}
