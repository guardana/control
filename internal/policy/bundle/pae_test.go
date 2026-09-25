package bundle_test

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/policy/bundle"
)

// The vector in the DSSE specification's "Test Vectors" section, typed as it
// is printed there. specPAE is held to it as well, because the property,
// verification and fuzz tests use specPAE as the second implementation.
func TestPAEMatchesTheSpecificationVector(t *testing.T) {
	const want = "DSSEv1 29 http://example.com/HelloWorld 11 hello world"
	body := []byte("hello world")
	if got := bundle.PAE("http://example.com/HelloWorld", body); string(got) != want {
		t.Errorf("PAE = %q, want %q", got, want)
	}
	if got := specPAE("http://example.com/HelloWorld", body); string(got) != want {
		t.Errorf("specPAE = %q, want %q", got, want)
	}
}

// Vectors the specification's own does not reach, each length counted by hand
// in bytes. The first body starts and ends with a space and holds a zero byte
// and the three-byte U+20AC, so it is 7 bytes and 5 characters: a length
// counted in characters, a trimmed body and a copy that stops at the zero byte
// each give other bytes. Its type is PayloadType, which pins the constant too.
func TestPAEHandTypedVectors(t *testing.T) {
	cases := []struct {
		name, payloadType, body, want string
	}{
		{
			name:        "the policy type and a body a careless copy changes",
			payloadType: bundle.PayloadType,
			body:        " a\x00\u20ac ",
			want:        "DSSEv1 33 application/vnd.agent-policy+json 7  a\x00\u20ac ",
		},
		{
			name:        "a type longer in bytes than in characters",
			payloadType: "caf\u00e9",
			body:        "x",
			want:        "DSSEv1 5 caf\u00e9 1 x",
		},
		{
			name:        "a length of two digits",
			payloadType: "t",
			body:        "0123456789",
			want:        "DSSEv1 1 t 10 0123456789",
		},
		{
			name:        "both empty",
			payloadType: "",
			body:        "",
			want:        "DSSEv1 0  0 ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundle.PAE(tc.payloadType, []byte(tc.body)); string(got) != tc.want {
				t.Errorf("PAE(%q, %q) = %q, want %q", tc.payloadType, tc.body, got, tc.want)
			}
		})
	}
	if got := bundle.PAE("", nil); string(got) != "DSSEv1 0  0 " {
		t.Errorf("PAE with a nil body = %q, want the empty body's encoding", got)
	}
}

// The result belongs to the caller: changing the body afterwards, or calling
// again, leaves it as it was.
func TestPAEReturnsAFreshSlice(t *testing.T) {
	body := []byte("abc")
	first := bundle.PAE("t", body)
	body[0] = 'X'
	second := bundle.PAE("u", []byte("zzzz"))
	if want := "DSSEv1 1 t 3 abc"; string(first) != want {
		t.Errorf("the first result is now %q, want %q", first, want)
	}
	if want := "DSSEv1 1 u 4 zzzz"; string(second) != want {
		t.Errorf("the second result is %q, want %q", second, want)
	}
}

// largePrefix is the encoding of a largeBody under PayloadType up to the body,
// typed: 7 bytes of "DSSEv1 ", 3 of "33 ", 34 of the type and a space, and 8
// of "1048577 ", 52 in all.
const largePrefix = "DSSEv1 33 application/vnd.agent-policy+json 1048577 "

// largeBody is one byte longer than the largest document the policy parser
// accepts, 1 MiB, so an encoding, a signature or a digest that stops at any
// length up to that bound misses its last byte. That byte differs from the
// others, so a copy that stops early does not end the way the body does.
func largeBody() []byte {
	b := bytes.Repeat([]byte{'a'}, 1<<20+1)
	b[len(b)-1] = 'z'
	return b
}

// The whole of a largeBody is in its encoding: the length is written in full,
// the result is as long as the formula gives, 52 + 1048577 bytes, and it ends
// with the body's last bytes.
func TestPAEEncodesTheWholeOfALargeBody(t *testing.T) {
	body := largeBody()
	got := bundle.PAE(bundle.PayloadType, body)
	if want := 52 + 1048577; len(got) != want {
		t.Errorf("len(PAE) over %d bytes = %d, want %d", len(body), len(got), want)
	}
	if !bytes.HasPrefix(got, []byte(largePrefix)) {
		t.Errorf("PAE starts %q, want %q", got[:min(len(got), len(largePrefix))], largePrefix)
	}
	if tail := body[len(body)-16:]; !bytes.HasSuffix(got, tail) {
		t.Errorf("PAE ends %q, want the body's last bytes %q", got[max(0, len(got)-16):], tail)
	}
	if !bytes.Equal(got, append([]byte(largePrefix), body...)) {
		t.Errorf("PAE over %d bytes is not the typed prefix followed by the whole body", len(body))
	}
}

// Over any type and body, PAE agrees with specPAE and reads back into the pair
// it came from. Reading back is what makes the encoding unambiguous: no two
// pairs share one, which is why DSSE signs it rather than the body.
func TestPAEAgreesWithTheFormulaAndReadsBack(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		payloadType := drawText(rt, "type")
		body := []byte(drawText(rt, "body"))
		got := bundle.PAE(payloadType, body)
		if want := specPAE(payloadType, body); !bytes.Equal(got, want) {
			rt.Fatalf("PAE(%q, %q) = %q, want %q", payloadType, body, got, want)
		}
		gotType, gotBody, ok := readPAE(got)
		if !ok || gotType != payloadType || !bytes.Equal(gotBody, body) {
			rt.Fatalf("PAE(%q, %q) = %q reads back as (%q, %q, %v)", payloadType, body, got, gotType, gotBody, ok)
		}
	})
}

// drawText draws arbitrary bytes or valid UTF-8, half each: random bytes alone
// rarely form a character whose length in bytes is not one.
func drawText(rt *rapid.T, label string) string {
	if rapid.Bool().Draw(rt, label+" is text") {
		return rapid.StringN(0, 40, 160).Draw(rt, label)
	}
	return string(rapid.SliceOfN(rapid.Byte(), 0, 200).Draw(rt, label))
}

// specPAE is a second implementation of the DSSE formula, written from the
// specification's text with fmt rather than strconv.
func specPAE(payloadType string, body []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(body), body)
}

// readPAE reads an encoding back into its type and body. It refuses what the
// grammar cannot produce: another tag, a length with a sign or a leading zero,
// a missing separator, and bytes left over.
func readPAE(pae []byte) (string, []byte, bool) {
	rest, ok := bytes.CutPrefix(pae, []byte("DSSEv1 "))
	if !ok {
		return "", nil, false
	}
	payloadType, rest, ok := readCounted(rest)
	if !ok {
		return "", nil, false
	}
	if rest, ok = bytes.CutPrefix(rest, []byte(" ")); !ok {
		return "", nil, false
	}
	body, rest, ok := readCounted(rest)
	if !ok || len(rest) != 0 {
		return "", nil, false
	}
	return string(payloadType), body, true
}

// readCounted reads a decimal byte count, a space, and that many bytes.
func readCounted(b []byte) (field, rest []byte, ok bool) {
	digits, rest, found := bytes.Cut(b, []byte(" "))
	if !found || len(digits) == 0 || (len(digits) > 1 && digits[0] == '0') {
		return nil, nil, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return nil, nil, false
		}
	}
	n, err := strconv.Atoi(string(digits))
	if err != nil || n > len(rest) {
		return nil, nil, false
	}
	return rest[:n], rest[n:], true
}
