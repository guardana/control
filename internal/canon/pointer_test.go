package canon

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// The pointer's two limits and its marker, written out rather than read from
// the implementation.
const (
	tokenCap   = 64
	pointerCap = 256
	marker     = "~..."
)

var quotedPointer = regexp.MustCompile(`at ("(?:[^"\\]|\\.)*")`)

// quotedPointerIn returns the JSON pointer a refusal names, as quoted.
func quotedPointerIn(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("want a refusal")
	}
	m := quotedPointer.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("refusal %q names no pointer", err)
	}
	return m[1]
}

// pointerIn returns the JSON pointer a refusal names, unquoted.
func pointerIn(t *testing.T, err error) string {
	t.Helper()
	quoted := quotedPointerIn(t, err)
	p, uerr := strconv.Unquote(quoted)
	if uerr != nil {
		t.Fatalf("pointer %s does not unquote: %v", quoted, uerr)
	}
	return p
}

// refusedUnder returns what both entry points say about a float nested under
// the given member names, outermost first.
func refusedUnder(keys ...string) (encoded, parsed error) {
	var v any = 1.5
	doc := "1.5"
	for i := len(keys) - 1; i >= 0; i-- {
		v = map[string]any{keys[i]: v}
		doc = "{" + strconv.Quote(keys[i]) + ":" + doc + "}"
	}
	_, encoded = Canonicalize(v)
	_, parsed = CanonicalizeJSON([]byte(doc))
	return encoded, parsed
}

// TestPointerTokenIsCapped: a member name is caller content with no length
// limit of its own, and a refusal travels into logs and evidence. A pointer
// keeps at most 64 bytes of each member name, cut on a character boundary and
// before escaping, and marks the cut with "~...": a tilde followed by anything
// but 0 or 1 occurs in no RFC 6901 pointer, so the marker never reads as a name.
func TestPointerTokenIsCapped(t *testing.T) {
	t.Parallel()

	kept := strings.Repeat("k", tokenCap)
	cases := []struct{ name, key, want string }{
		{"at the cap", kept, "/" + kept},
		{"one byte over", kept + "k", "/" + kept + marker},
		{"60 KB", strings.Repeat("k", 60000), "/" + kept + marker},
		// The 32nd e-acute would straddle byte 64, so 31 of them are kept.
		{"a character across the cut", "a" + strings.Repeat("\xc3\xa9", 32), "/a" + strings.Repeat("\xc3\xa9", 31) + marker},
		// 64 bytes of "~/" are kept, and render as 128.
		{"escapes before the cut", strings.Repeat("~/", 40), "/" + strings.Repeat("~0~1", 32) + marker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, parsed := refusedUnder(tc.key)
			for entry, err := range map[string]error{"Canonicalize": encoded, "CanonicalizeJSON": parsed} {
				if got := pointerIn(t, err); got != tc.want {
					t.Errorf("%s: pointer %q, want %q", entry, got, tc.want)
				}
			}
		})
	}
}

// TestPointerIsCapped: 32 member names of 64 bytes would still make a pointer
// of two kilobytes, so the whole pointer keeps the member names that fit in 256
// bytes and ends in the marker when one is left out.
func TestPointerIsCapped(t *testing.T) {
	t.Parallel()

	a, b, c, d := strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 63)
	whole := "/" + a + "/" + b + "/" + c + "/" + d
	if len(whole) != pointerCap {
		t.Fatalf("four names of 63 bytes render as %d bytes, want %d", len(whole), pointerCap)
	}
	cases := []struct {
		name string
		keys []string
		want string
	}{
		{"at the cap", []string{a, b, c, d}, whole},
		// A 64-byte name is whole on its own, and one byte too many here.
		{"one byte over", []string{a, b, c, d + "d"}, "/" + a + "/" + b + "/" + c + "/" + marker},
		{"deeper", []string{a, b, c, d, a, b, c, d}, whole + "/" + marker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, parsed := refusedUnder(tc.keys...)
			for entry, err := range map[string]error{"Canonicalize": encoded, "CanonicalizeJSON": parsed} {
				if got := pointerIn(t, err); got != tc.want {
					t.Errorf("%s: pointer %q, want %q", entry, got, tc.want)
				}
			}
		})
	}
}

// TestLongKeyRefusalIsBounded: the bound on a refusal is not the pointer's:
// %q renders a control byte or DEL in a member name as four, so a pointer of
// DEL at the cap quotes to four times its length.
func TestLongKeyRefusalIsBounded(t *testing.T) {
	t.Parallel()

	// A pointer is at most 261 bytes before quoting: the names that fit in
	// 256, then a separator and the marker when one is left out. Quoted, at
	// most four times that inside its quotes; the rest of a refusal is fixed
	// text, and the arguments key and a member number fit in 200 bytes.
	const (
		rawPointerCap    = pointerCap + 1 + len(marker)
		quotedPointerCap = 4*rawPointerCap + 2
		refusalCap       = quotedPointerCap + 200
	)
	del := strings.Repeat("\x7f", 63)
	delDoc := `{"` + del + `":{"` + del + `":{"` + del + `":{"` + del + `":0.5}}}}`
	// The 1 MiB document is over the digest's size bound, so only the parser
	// sees its key.
	cases := []struct {
		name      string
		doc       string
		cut       bool
		viaDigest bool
	}{
		{"a float under a 60 KB key", `{"alice@example.test/` + strings.Repeat("k", 60000) + `":0.5}`, true, true},
		{"a duplicate under a 1 MiB key", `{"` + strings.Repeat("K", 1<<20) + `":{"a":1,"a":2}}`, true, false},
		{"a float under 63 DEL bytes at four levels", delDoc, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, parsed := CanonicalizeJSON([]byte(tc.doc))
			entries := map[string]error{"CanonicalizeJSON": parsed}
			if tc.viaDigest {
				_, entries["DigestV1"] = DigestV1(&controlv1.ActionEnvelope{}, []byte(tc.doc))
			}
			for entry, err := range entries {
				if !errors.Is(err, ErrUnsupportedValue) {
					t.Errorf("%s: err = %v, want ErrUnsupportedValue", entry, err)
					continue
				}
				if len(err.Error()) > refusalCap {
					t.Errorf("%s: the refusal is %d bytes, want at most %d", entry, len(err.Error()), refusalCap)
				}
				if quoted := quotedPointerIn(t, err); len(quoted) > quotedPointerCap {
					t.Errorf("%s: the quoted pointer is %d bytes, want at most %d", entry, len(quoted), quotedPointerCap)
				}
				if raw := pointerIn(t, err); len(raw) > rawPointerCap {
					t.Errorf("%s: the pointer is %d bytes, want at most %d", entry, len(raw), rawPointerCap)
				}
				if got := strings.Contains(err.Error(), marker); got != tc.cut {
					t.Errorf("%s: refusal %.120q marks a cut: %t, want %t", entry, err, got, tc.cut)
				}
			}
		})
	}
	// The DEL case is at the pointer cap and every DEL byte quotes to four, so
	// the quoted bound is the real one rather than a loose number.
	_, err := CanonicalizeJSON([]byte(delDoc))
	floor := 4 * strings.Count(delDoc, "\x7f")
	if quoted := quotedPointerIn(t, err); len(quoted) < floor {
		t.Errorf("the DEL pointer quotes to %d bytes, want at least %d", len(quoted), floor)
	}
}
