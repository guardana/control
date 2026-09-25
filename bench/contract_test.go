// Benchmarks for the two calls that sit on the path an action takes before
// anything has authorized it: bounded validation of an envelope, and the
// canonical action digest an approval binds to.
//
// Both read the golden digest fixtures, so the two numbers are measured over
// the same envelopes and can be read against each other. Those fixtures are
// also the only envelopes in the tree whose validity and whose digest are both
// pinned by a test, which is what lets this package refuse to measure a
// refusal: both functions return early when they reject their input, so a
// fixture that stopped validating would be reported as a fast validation and a
// fast digest rather than as a failure.
package bench_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/pkg/contract"
)

// The fixtures are at the repository root, not beside a package: they are the
// contract a later implementation in another language is checked against.
const fixtureDir = "../testdata/digest"

// A function rather than a package-level slice, so nothing here holds mutable
// state a benchmark could leave changed for the next one.
func fixtureNames() []string {
	return []string{"minimal", "refund_prod", "delegated", "mutated_amount"}
}

// subject is one measured input: an envelope that has already parsed and
// validated, the authorized arguments beside it, and the digest the fixture
// pins for the two together.
type subject struct {
	name   string
	env    *controlv1.ActionEnvelope
	args   []byte
	digest string
}

// subjects loads every fixture and fails on anything the measured path would
// reject. Nothing is skipped: a corpus that quietly shrank would still produce
// a number, and a number over fewer inputs than the page claims is worse than
// no number.
func subjects(tb testing.TB) []subject {
	tb.Helper()

	// Rooted at the fixture directory so a name cannot send the read anywhere
	// else, which is also what keeps gosec from having to guess.
	fsys := os.DirFS(fixtureDir)
	names := fixtureNames()
	loaded := make([]subject, 0, len(names))
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name+".json")
		if err != nil {
			tb.Fatalf("read fixture %s: %v", name, err)
		}
		var f struct {
			Envelope       json.RawMessage `json:"envelope"`
			AuthorizedArgs json.RawMessage `json:"authorized_args"`
			ExpectedDigest string          `json:"expected_digest"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			tb.Fatalf("decode fixture %s: %v", name, err)
		}
		// DecodeJSON validates as well as parses, so a fixture that the frozen
		// contract refuses never reaches a timed loop.
		env, err := contract.DecodeJSON(f.Envelope)
		if err != nil {
			tb.Fatalf("fixture %s does not validate: %v", name, err)
		}
		loaded = append(loaded, subject{name: name, env: env, args: f.AuthorizedArgs, digest: f.ExpectedDigest})
	}
	if len(loaded) == 0 {
		tb.Fatal("no fixture loaded; a benchmark over an empty corpus reports a time for nothing")
	}
	return loaded
}

// TestBenchmarkedPathSucceeds is what makes `go test ./bench/` mean something.
// A package holding only benchmarks reports ok having run nothing, and both
// functions below are at their fastest on input they refuse, so the claim that
// the numbers describe successful work needs a check that can fail.
//
// It also logs the encoded size of each envelope, because a duration is not
// readable without the size of the message it was measured over, and a number
// on a page needs something in the repository behind it.
func TestBenchmarkedPathSucceeds(t *testing.T) {
	for _, s := range subjects(t) {
		t.Run(s.name, func(t *testing.T) {
			if err := contract.Validate(s.env); err != nil {
				t.Errorf("Validate: %v", err)
			}
			got, err := canon.DigestV1(s.env, s.args)
			if err != nil {
				t.Fatalf("DigestV1: %v", err)
			}
			if got != s.digest {
				t.Errorf("digest = %s, want %s", got, s.digest)
			}
			t.Logf("envelope %d bytes encoded, authorized arguments %d bytes",
				proto.Size(s.env), len(s.args))
		})
	}
}

// BenchmarkValidateEnvelope measures Validate over an envelope that has already
// been parsed. The parse is deliberately outside it: Decode and DecodeJSON bound
// the bytes before parsing, and mixing a protojson decode into this number would
// measure the codec rather than the contract's own checks.
func BenchmarkValidateEnvelope(b *testing.B) {
	for _, s := range subjects(b) {
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := contract.Validate(s.env); err != nil {
					b.Fatalf("Validate: %v", err)
				}
			}
		})
	}
}

// BenchmarkDigestV1 measures the whole digest: the ADR-0005 field extraction,
// the canonical JSON encoding of the resulting action, and one sha256 over the
// domain tag and those bytes.
//
// The digest string is discarded rather than compared. b.Loop keeps the call
// from being optimized away, the error is still read, and the value is checked
// by the test above, so a comparison here would only put a string compare
// inside the number.
func BenchmarkDigestV1(b *testing.B) {
	for _, s := range subjects(b) {
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := canon.DigestV1(s.env, s.args); err != nil {
					b.Fatalf("DigestV1: %v", err)
				}
			}
		})
	}
}
