package trust_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

// private is the test key called name, derived from its name so that no key
// text stands in the tree.
func private(name string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("trust-test/" + name))
	return ed25519.NewKeyFromSeed(seed[:])
}

func public(name string) ed25519.PublicKey { return private(name).Public().(ed25519.PublicKey) }

func idOf(name string) string { return policykey.KeyID(public(name)) }

// issued and expires are the times every roster of these tests carries unless
// a test names others.
const (
	issued  = "2026-10-30T00:00:00Z"
	expires = "2026-10-31T00:00:00Z"
)

func clockAt(t testing.TB, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// keyEntry is the roster entry of the operator key called name, granted
// operate over t1/p1.
func keyEntry(name string) map[string]any {
	return map[string]any{
		"keyId": idOf(name), "alg": "ed25519", "role": "operator", "subject": name,
		"publicKey": policykey.FormatPublic(public(name)),
		"grants":    []any{map[string]any{"tenant": "t1", "project": "p1", "level": "operate"}},
	}
}

// doc is a roster body of roster-a at serial naming the keys called names.
func doc(serial int64, names ...string) map[string]any {
	keys := make([]any, 0, len(names))
	for _, n := range names {
		keys = append(keys, keyEntry(n))
	}
	return map[string]any{
		"kind": trust.DocumentKind, "rosterId": "roster-a", "serial": serial,
		"issuedAt": issued, "expiresAt": expires, "keys": keys,
	}
}

func canonical(t testing.TB, v any) []byte {
	t.Helper()
	body, err := canon.Canonicalize(v)
	if err != nil {
		t.Fatalf("canonicalizing a body this test builds: %v", err)
	}
	return body
}

// signBody signs body as it is, under payloadType, with the key called
// signer, and names that key's id.
func signBody(t testing.TB, payloadType string, body []byte, signer string) trust.Envelope {
	t.Helper()
	sig, err := bundle.SignBytesAs(payloadType, body, private(signer))
	if err != nil {
		t.Fatal(err)
	}
	return trust.Envelope{PayloadType: payloadType, Payload: body, Signatures: []trust.Signature{{KeyID: idOf(signer), Signature: sig}}}
}

// signed is doc signed by the root as a roster.
func signed(t testing.TB, d map[string]any) trust.Envelope {
	t.Helper()
	return signBody(t, trust.PayloadType, canonical(t, d), "root")
}

func writeRoster(t testing.TB, path string, env trust.Envelope) {
	t.Helper()
	raw, err := trust.MarshalFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// rig is a roster file, an authority floor listing roster-a, and a clock the
// test moves.
type rig struct {
	path, floor string
	now         time.Time
	roster      trust.Roster
}

func newRig(t testing.TB, first trust.Envelope, others ...ed25519.PublicKey) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{path: filepath.Join(dir, "roster.json"), floor: filepath.Join(dir, "floor"), now: clockAt(t, "2026-10-30T12:00:00Z")}
	if err := policystate.InitAuthority(t.Context(), r.floor, "roster-a"); err != nil {
		t.Fatal(err)
	}
	writeRoster(t, r.path, first)
	roster, err := trust.Open(t.Context(), trust.Config{
		Root: public("root"), Path: r.path, FloorDir: r.floor, Others: others,
		Clock: func() time.Time { return r.now },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r.roster = roster
	return r
}

func (r *rig) lookup(t testing.TB, name string) trust.Answer {
	t.Helper()
	return r.roster.Lookup(t.Context(), idOf(name))
}
