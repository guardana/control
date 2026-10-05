package secretscan_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

// decodedTexts is the oracle's own walk: every string and key, as decoded.
func decodedTexts(t *testing.T, doc []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var out []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("oracle walk of a valid document: %v", err)
		}
		if s, ok := tok.(string); ok {
			out = append(out, s)
		}
	}
}

func FuzzScanJSON(f *testing.F) {
	f.Add([]byte(`{"content":[{"type":"text","text":"denied for opensesame-42"}]}`), "opensesame-42")
	f.Add([]byte(`{"opensesame-42":true}`), "opensesame-42")
	f.Add([]byte(`{"t":"{\"x\":\"pw\\\"x\\\\y\\u003cz\"}"}`), "pw\"x\\y<z")
	f.Add([]byte(`["b3BlbnNlc2FtZS00Mg==", "q=open+sesame%2042"]`), "open sesame 42")
	f.Add([]byte(`{"n":12345678901e5}`), "12345678901")
	f.Add([]byte(`{"a":1} trailing`), "a")
	f.Add([]byte(`[1,]`), "1")
	f.Add([]byte(`"\ud83d\ude00 \u00e9"`), "😀")
	f.Add([]byte(`{"t":"ab\\u003ccd&ef","u":"%5Cu0026"}`), "ab<cd&ef")
	f.Add([]byte{}, "")
	f.Fuzz(func(t *testing.T, doc []byte, secret string) {
		set, err := secretscan.New([]secretscan.Secret{{Key: "fuzz.key", Value: secret, Kind: secretscan.Credential}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got := set.ScanJSON(doc)
		if v := set.ScanText(string(doc)); v.State == secretscan.Unscanned {
			t.Fatal("a text was not scanned")
		}
		if !json.Valid(doc) {
			if got != (secretscan.Verdict{}) {
				t.Fatalf("unparsable document scanned as %+v", got)
			}
			return
		}
		checkValidDocument(t, doc, secret, got)
	})
}

func checkValidDocument(t *testing.T, doc []byte, secret string, got secretscan.Verdict) {
	t.Helper()
	if got.State == secretscan.Unscanned {
		t.Fatal("a valid document was not scanned")
	}
	if got.State == secretscan.Found && got.Key != "fuzz.key" {
		t.Fatalf("found under key %q", got.Key)
	}
	if secret == "" {
		if got.State != secretscan.Clean {
			t.Fatalf("an empty value was found: %+v", got)
		}
		return
	}
	for _, s := range decodedTexts(t, doc) {
		if strings.Contains(s, secret) && got.State != secretscan.Found {
			t.Fatalf("secret is in %q but the verdict is %+v", s, got)
		}
		var inner string
		if json.Unmarshal([]byte(`"`+s+`"`), &inner) == nil && strings.Contains(inner, secret) && got.State != secretscan.Found {
			t.Fatalf("secret is in %q JSON-unescaped but the verdict is %+v", s, got)
		}
	}
}
