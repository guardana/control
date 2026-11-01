package operators_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/operators"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

func private(name string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("operators-test/" + name))
	return ed25519.NewKeyFromSeed(seed[:])
}

func public(name string) ed25519.PublicKey { return private(name).Public().(ed25519.PublicKey) }

func idOf(name string) string { return policykey.KeyID(public(name)) }

// grant is one grant of a roster entry: tenant, project and level.
type grant [3]string

// entry is the roster entry of the key called key, for subject, with grants.
func entry(key, subject string, grants ...grant) map[string]any {
	gs := make([]any, 0, len(grants))
	for _, g := range grants {
		gs = append(gs, map[string]any{"tenant": g[0], "project": g[1], "level": g[2]})
	}
	return map[string]any{
		"keyId": idOf(key), "alg": "ed25519", "role": "operator", "subject": subject,
		"publicKey": policykey.FormatPublic(public(key)), "grants": gs,
	}
}

// standard is the roster most tests use: alice operates t1/A, bob operates
// t1/B and reads t1/A, carol administers t1/A.
func standard() []any {
	return []any{
		entry("alice", "alice", grant{"t1", "A", "operate"}),
		entry("bob", "bob", grant{"t1", "B", "operate"}, grant{"t1", "A", "read"}),
		entry("carol", "carol", grant{"t1", "A", "admin"}),
	}
}

func rosterBody(t testing.TB, serial int64, keys []any) []byte {
	t.Helper()
	body, err := canon.Canonicalize(map[string]any{
		"kind": trust.DocumentKind, "rosterId": "roster-a", "serial": serial,
		"issuedAt": "2026-10-30T00:00:00Z", "expiresAt": "2026-10-31T00:00:00Z", "keys": keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// sign signs body under payloadType with the key called signer, naming
// keyid as the signature's key id.
func sign(t testing.TB, payloadType string, body []byte, signer, keyid string) operators.Envelope {
	t.Helper()
	sig, err := bundle.SignBytesAs(payloadType, body, private(signer))
	if err != nil {
		t.Fatal(err)
	}
	return operators.Envelope{PayloadType: payloadType, Payload: body, Signatures: []trust.Signature{{KeyID: keyid, Signature: sig}}}
}

// world is a roster file the root signed, its floor, and a roster port over
// them at a fixed clock.
type world struct {
	path   string
	roster trust.Roster
}

func newWorld(t testing.TB, keys []any) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{path: filepath.Join(dir, "roster.json")}
	floor := filepath.Join(dir, "floor")
	if err := policystate.InitAuthority(t.Context(), floor, "roster-a"); err != nil {
		t.Fatal(err)
	}
	w.write(t, 1, keys)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	r, err := trust.Open(t.Context(), trust.Config{Root: public("root"), RosterID: "roster-a", Path: w.path, FloorDir: floor, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	w.roster = r
	return w
}

func (w *world) write(t testing.TB, serial int64, keys []any) {
	t.Helper()
	raw, err := trust.MarshalFile(sign(t, trust.PayloadType, rosterBody(t, serial, keys), "root", idOf("root")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// answer is an approval answer body signed by the key called signer, which
// names itself in alg and keyId.
func answer(t testing.TB, signer string) operators.Envelope {
	t.Helper()
	return answerWith(t, signer, map[string]any{"alg": "ed25519", "keyId": idOf(signer), "decision": "approve"})
}

func answerWith(t testing.TB, signer string, members map[string]any) operators.Envelope {
	t.Helper()
	body, err := canon.Canonicalize(members)
	if err != nil {
		t.Fatal(err)
	}
	return sign(t, operators.AnswerPayloadType, body, signer, idOf(signer))
}

func authenticator(t testing.TB, r trust.Roster, b operators.Bounds) operators.Authenticator {
	t.Helper()
	a, err := operators.NewAuthenticator("", r, b)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func authorizer(t testing.TB, r trust.Roster, b operators.Bounds) operators.Authorizer {
	t.Helper()
	a, err := operators.NewAuthorizer("", r, b)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// authenticated is signer's answer authenticated under w's roster.
func authenticated(t testing.TB, w *world, signer string) operators.Authentication {
	t.Helper()
	who := authenticator(t, w.roster, operators.Bounds{}).Authenticate(t.Context(), operators.ActApprove, answer(t, signer))
	if who.State() != operators.AuthAuthenticated {
		t.Fatalf("%s's answer was not authenticated: %q", signer, who.Cause())
	}
	return who
}

// fakeRoster answers what answer returns, after waiting until release is
// closed when it is not nil, ignoring the context when deaf.
type fakeRoster struct {
	answer  trust.Answer
	release chan struct{}
	deaf    bool
}

func (f *fakeRoster) Lookup(ctx context.Context, _ string) trust.Answer {
	if f.release != nil {
		if f.deaf {
			<-f.release
		} else {
			select {
			case <-f.release:
			case <-ctx.Done():
			}
		}
	}
	return f.answer
}

func (f *fakeRoster) Status(context.Context) trust.Status { return trust.Status{} }
