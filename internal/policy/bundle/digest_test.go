package bundle_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/policy/bundle"
)

// Published SHA-256 values, typed as printed: the zero-length message of
// NIST's SHA-256 short-message test vectors, and "abc", the 448-bit message
// and one million "a" of FIPS 180-2, Appendix B. "abc" and a line feed has no
// published value; two SHA-256 implementations other than Go's agree on the
// one typed here. The bundle digest is the hash of the bytes with no tag in
// front (ADR-0011), so a tag, another input or uppercase hex misses each of
// these, and so does a digest that stops after a few KiB or drops a final line
// feed.
func TestDigestMatchesPublishedSHA256Values(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"the empty message", "", "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{`"abc"`, "abc", "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{
			"the 448-bit message",
			"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
			"sha256:248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
		},
		{
			`one million "a"`,
			strings.Repeat("a", 1_000_000),
			"sha256:cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0",
		},
		{
			`"abc" and a line feed`,
			"abc\n",
			"sha256:edeaaff3f1774ad2888673770c6d64097e391bc362d7d6fb34982ddf0efd18cb",
		},
	}
	for _, tc := range cases {
		if got := bundle.Digest([]byte(tc.in)); got != tc.want {
			t.Errorf("Digest(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := bundle.Digest(nil); got != cases[0].want {
		t.Errorf("Digest(nil) = %q, want the empty message's %q", got, cases[0].want)
	}
}

// Two largeBody inputs apart only in their last byte have different digests.
// A digest that stopped at any length up to the parser's bound would give
// them one, and one digest names one bundle.
func TestDigestCoversTheLastByteOfALargeInput(t *testing.T) {
	a := largeBody()
	b := bytes.Clone(a)
	b[len(b)-1]++
	if bundle.Digest(a) == bundle.Digest(b) {
		t.Fatalf("Digest ignores byte %d of %d", len(a)-1, len(a))
	}
}

// Over any bytes the digest has one shape, and a one-bit change anywhere in
// the input changes it, so no byte is left out of what it covers.
func TestDigestShapeAndCoverage(t *testing.T) {
	shape := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	rapid.Check(t, func(rt *rapid.T) {
		body := rapid.SliceOfN(rapid.Byte(), 1, 512).Draw(rt, "body")
		got := bundle.Digest(body)
		if !shape.MatchString(got) {
			rt.Fatalf("Digest(%q) = %q, not sha256: and 64 lowercase hex digits", body, got)
		}
		changed := bytes.Clone(body)
		bit := rapid.IntRange(0, 8*len(changed)-1).Draw(rt, "bit")
		changed[bit/8] ^= 1 << (bit % 8)
		if bundle.Digest(changed) == got {
			rt.Fatalf("Digest ignores bit %d of %q", bit, body)
		}
	})
}
