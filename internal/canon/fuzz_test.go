package canon

import (
	"bytes"
	"encoding/json"
	"strconv"
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
		"0.7", "1e-7", "5e-324", "4.9e-324", "1e400", "-0.0", "1e-99999999999999999999",
		"9007199254740991.0", "0.30000000000000004", `{"ID":1,"id":2}`,
		"\"\xff\"", "{\"\xff\":1}", "\"\\ud800\\ud800\"", "\"\\udfff\"",
	} {
		f.Add([]byte(extra))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		checkDefinitionForm(t, raw)
		out, err := CanonicalizeJSON(raw)
		if err != nil {
			if out != nil {
				t.Fatalf("CanonicalizeJSON(%q) returned %q with error %v", raw, out, err)
			}
			return
		}
		checkFormsAgree(t, raw, out)

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

// checkFormsAgree: a document the action form accepts has the same bytes in
// the definition form, so a definition of integers has the fingerprint of its
// action-form bytes.
func checkFormsAgree(t *testing.T, raw, out []byte) {
	t.Helper()
	if definition, err := FingerprintJSON(raw); err != nil || !bytes.Equal(definition, out) {
		t.Fatalf("FingerprintJSON(%q) = %q, %v; CanonicalizeJSON gives %q", raw, definition, err, out)
	}
}

// checkDefinitionForm holds the definition form to the same output
// properties: valid JSON, no whitespace, a fixed point.
func checkDefinitionForm(t *testing.T, raw []byte) {
	t.Helper()
	out, err := FingerprintJSON(raw)
	if err != nil {
		if out != nil {
			t.Fatalf("FingerprintJSON(%q) returned %q with error %v", raw, out, err)
		}
		return
	}
	if !json.Valid(out) || bytes.ContainsAny(out, "\n\t\r") {
		t.Fatalf("FingerprintJSON(%q) = %q, which is not canonical JSON", raw, out)
	}
	again, err := FingerprintJSON(out)
	if err != nil || !bytes.Equal(out, again) {
		t.Fatalf("FingerprintJSON(%q) is not a fixed point: %q, then %q, %v", raw, out, again, err)
	}
}

// FuzzSafeIntegers: every integer inside the JSON-safe range is written as its
// decimal digits in both forms, and every other spelling of it gives the same
// bytes.
func FuzzSafeIntegers(f *testing.F) {
	for _, n := range []int64{0, 1, -1, 42, 1 << 52, safeMax, safeMin, 1e15, -123456789} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n int64) {
		n %= safeMax + 1
		want := strconv.FormatInt(n, 10)
		for _, literal := range []string{want, want + ".0", want + "e0", want + "E-0", want + ".000E+0"} {
			for name, canonicalize := range map[string]func([]byte) ([]byte, error){
				"CanonicalizeJSON": CanonicalizeJSON, "FingerprintJSON": FingerprintJSON,
			} {
				got, err := canonicalize([]byte(literal))
				if err != nil || string(got) != want {
					t.Fatalf("%s(%s) = %s, %v; want %s", name, literal, got, err, want)
				}
			}
		}
		if got, err := Canonicalize(n); err != nil || string(got) != want {
			t.Fatalf("Canonicalize(%d) = %s, %v", n, got, err)
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
