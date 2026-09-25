package canon_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// emptyArgumentsHash is the hash of absent arguments, computed outside Go, by
// shasum and by node, and written out so that no code under test supplies it:
//
//	printf 'agent-arguments-hash/v1\n{}' | shasum -a 256
const emptyArgumentsHash = "sha256:6fdd0e8a870307187078a787323b1237e1ebb5d9d1998f9a5a937001920da409"

// argumentsFixture is the on-disk shape of the arguments hash golden. The
// document stays raw, for the reason fixture's does.
type argumentsFixture struct {
	AuthorizedArgs json.RawMessage `json:"authorized_args"`
	ExpectedHash   string          `json:"expected_hash"`
}

func loadArgumentsFixture(t testing.TB) argumentsFixture {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(fixtureDir), "arguments_hash.json")
	if err != nil {
		t.Fatalf("read the arguments hash fixture: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f argumentsFixture
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode the arguments hash fixture: %v", err)
	}
	if len(f.AuthorizedArgs) == 0 {
		t.Fatal("the arguments hash fixture has no authorized_args")
	}
	return f
}

// TestArgumentsHashGolden pins the arguments hash as TestGoldenFixtures pins the
// digest: the value was reported by this test for a fixture with no
// expected_hash, reproduced by a second implementation written from the README,
// and pasted in.
func TestArgumentsHashGolden(t *testing.T) {
	f := loadArgumentsFixture(t)
	got, err := canon.ArgumentsHashV1(f.AuthorizedArgs)
	if err != nil {
		t.Fatalf("ArgumentsHashV1: %v", err)
	}
	if f.ExpectedHash == "" {
		t.Fatalf("fixture has no expected_hash; computed %s", got)
	}
	if got != f.ExpectedHash {
		t.Errorf("hash = %s, want %s", got, f.ExpectedHash)
	}
	assertDigestForm(t, got)

	// sha256 over the tag and the canonical bytes, and nothing else. The file
	// spells the document another way, so a hash over its own bytes differs.
	body, err := canon.CanonicalizeJSON(f.AuthorizedArgs)
	if err != nil {
		t.Fatalf("CanonicalizeJSON: %v", err)
	}
	if bytes.Equal(body, f.AuthorizedArgs) {
		t.Fatal("the fixture's arguments are canonical already, so canonicalizing them is not under test")
	}
	sum := sha256.Sum256(append([]byte(argumentsTag), body...))
	if want := "sha256:" + hex.EncodeToString(sum[:]); got != want {
		t.Errorf("hash = %s, but sha256(tag||canonical form) = %s", got, want)
	}
}

// TestArgumentsHashIsAFunctionOfTheValue: another spelling of the same document
// has the same hash, and one changed value gives another.
func TestArgumentsHashIsAFunctionOfTheValue(t *testing.T) {
	f := loadArgumentsFixture(t)
	want := mustHash(t, f.AuthorizedArgs)
	if got := mustHash(t, reformat(t, f.AuthorizedArgs)); got != want {
		t.Errorf("the document spelled another way: hash = %s, want %s", got, want)
	}
	changed := bytes.Replace(f.AuthorizedArgs, []byte("-42"), []byte("-43"), 1)
	if bytes.Equal(changed, f.AuthorizedArgs) {
		t.Fatal("the fixture holds no -42 to change")
	}
	if got := mustHash(t, changed); got == want {
		t.Error("a changed value kept the hash")
	}
}

func mustHash(t testing.TB, args []byte) string {
	t.Helper()
	got, err := canon.ArgumentsHashV1(args)
	if err != nil {
		t.Fatalf("ArgumentsHashV1: %v", err)
	}
	return got
}

// TestArgumentsHashOfAbsentArguments pins "{}" when there are none: absent
// arguments and every spelling of the empty object share one hash, whose
// preimage is the tag and those two bytes.
func TestArgumentsHashOfAbsentArguments(t *testing.T) {
	sum := sha256.Sum256([]byte(argumentsTag + "{}"))
	if want := "sha256:" + hex.EncodeToString(sum[:]); want != emptyArgumentsHash {
		t.Fatalf("the pinned %s is not sha256 over the stated preimage, which is %s", emptyArgumentsHash, want)
	}
	for name, args := range map[string][]byte{
		"nil": nil, "empty": {}, "the empty object": []byte(`{}`), "spaced": []byte(" { } "),
	} {
		got, err := canon.ArgumentsHashV1(args)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != emptyArgumentsHash {
			t.Errorf("%s: hash = %s, want %s", name, got, emptyArgumentsHash)
		}
	}
}

// TestArgumentsHashRefusesWhatTheDigestRefuses: the hash and the digest read the
// arguments through one path, so they refuse the same documents in the same
// words and accept the same ones at both bounds. A receiver comparing the hash
// can then never accept a document the digest cannot cover.
func TestArgumentsHashRefusesWhatTheDigestRefuses(t *testing.T) {
	env := &controlv1.ActionEnvelope{}
	for _, doc := range []string{
		`{"temperature":0.7}`, `{"n":9007199254740992}`, `{"amount":1,"amount":2}`,
		`{"amount":1,"AMOUNT":2}`, `{"s":"\ud800"}`, `null`, "{\"s\":\"\xff\"}", `{"a":1} {"b":2}`, `{`,
		strings.Repeat("[", 33) + strings.Repeat("]", 33),
		`{"a":"` + strings.Repeat("x", 65529) + `"}`,
		// Only an empty byte string is absent: whitespace alone is not, and a
		// byte order mark is not stripped, though most readers would.
		"   ", "\xef\xbb\xbf{}",
	} {
		_, hashErr := canon.ArgumentsHashV1([]byte(doc))
		_, digestErr := canon.DigestV1(env, []byte(doc))
		switch {
		case hashErr == nil || digestErr == nil:
			t.Errorf("%.40q: hash err = %v, digest err = %v, want both refused", doc, hashErr, digestErr)
		case hashErr.Error() != digestErr.Error():
			t.Errorf("%.40q: the hash says %q, the digest %q", doc, hashErr, digestErr)
		}
	}
	for _, doc := range []string{
		strings.Repeat("[", 32) + strings.Repeat("]", 32),
		`{"a":"` + strings.Repeat("x", 65528) + `"}`,
	} {
		if _, err := canon.ArgumentsHashV1([]byte(doc)); err != nil {
			t.Errorf("%.40q was refused: %v", doc, err)
		}
	}
}
