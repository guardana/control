package canon

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// FuzzCanonicalizeJSON drives the parser and the encoder from raw bytes, which
// is where a panic would live: the digest reads whatever an adapter hands it,
// so a crash here is a way to stop an enforcement decision from happening.
//
// The properties are the ones a second implementation depends on. Whatever
// comes out is valid UTF-8 and valid JSON, it carries no whitespace between
// tokens, and it is a fixed point: canonicalizing canonical bytes returns them
// unchanged, or two producers of the same action could disagree on its digest.
func FuzzCanonicalizeJSON(f *testing.F) {
	for _, tc := range canonicalizeJSONCases {
		f.Add([]byte(tc.raw))
		f.Add([]byte(tc.want))
	}
	for _, tc := range canonicalizeJSONRejections {
		f.Add([]byte(tc.raw))
	}
	// Shapes the tables above do not reach: the depth limit from both sides,
	// long numbers, and bytes that are not valid UTF-8.
	for _, extra := range []string{
		"", "{}", "[]", `{"":""}`, `{"a":{"b":[1,{"c":null}]}}`,
		nestedJSON(depthMax), nestedJSON(depthMax + 1),
		"00", "-", "1e", "0e0", "9007199254740991000000",
		"\"\xff\"", "{\"\xff\":1}", "\"\\ud800\\ud800\"", "\"\\udfff\"",
	} {
		f.Add([]byte(extra))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		out, err := CanonicalizeJSON(raw)
		if err != nil {
			if out != nil {
				t.Fatalf("CanonicalizeJSON(%q) returned %q with error %v", raw, out, err)
			}
			return
		}

		if !utf8.Valid(out) {
			t.Fatalf("CanonicalizeJSON(%q) = %q, which is not valid UTF-8", raw, out)
		}
		if !json.Valid(out) {
			t.Fatalf("CanonicalizeJSON(%q) = %q, which is not valid JSON", raw, out)
		}
		if bytes.ContainsAny(out, "\n\t\r") {
			t.Fatalf("CanonicalizeJSON(%q) = %q, which carries whitespace between tokens", raw, out)
		}

		again, err := CanonicalizeJSON(out)
		if err != nil {
			t.Fatalf("CanonicalizeJSON refused its own output %q: %v", out, err)
		}
		if !bytes.Equal(out, again) {
			t.Fatalf("CanonicalizeJSON(%q) is not a fixed point\n first %q\nsecond %q", raw, out, again)
		}
	})
}

func nestedJSON(depth int) string {
	var b bytes.Buffer
	for range depth {
		b.WriteByte('[')
	}
	b.WriteByte('1')
	for range depth {
		b.WriteByte(']')
	}
	return b.String()
}
