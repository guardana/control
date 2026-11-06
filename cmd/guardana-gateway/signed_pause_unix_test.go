//go:build unix

package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/operators"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

// signedPauseTree is signedPlaneTree with a pause file signed by alice for
// acme/orders in a directory of its own, and its pause floor.
func signedPauseTree(t *testing.T) tree {
	t.Helper()
	tr := signedPlaneTree(t, "roster-a", []ed25519.PublicKey{signedTestPublic("alice")}, "roster-a")
	if err := os.Mkdir(filepath.Join(tr.dir, "pause"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := policystate.InitPause(t.Context(), filepath.Join(tr.dir, "pausefloor"), "acme", "orders"); err != nil {
		t.Fatal(err)
	}
	s := aliceSigner()
	if err := pause.InitSigned(t.Context(), filepath.Join(tr.dir, "pause", "pause.json"),
		pause.Header{KeyID: s.KeyID, Tenant: "acme", Project: "orders", Serial: 1}, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tr.config)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, tr.config, append(raw, []byte("\npause:\n  file: pause/pause.json\n  floor_dir: pausefloor\n")...))
	return tr
}

// aliceSigner signs as the operator alice of signedPlaneTree.
func aliceSigner() pause.Signer {
	alice := signedTestKey("alice")
	return pause.Signer{KeyID: policykey.KeyID(signedTestPublic("alice")), Sign: func(payloadType string, body []byte) ([]byte, error) {
		return bundle.SignBytesAs(payloadType, body, alice)
	}}
}

// TestASignedStartRefusesAPauseFileThePlanesAccountMayWrite: a pause file or
// directory the plane's account may write refuses the start, naming the key;
// placed apart, the plane serves alice's signed file.
func TestASignedStartRefusesAPauseFileThePlanesAccountMayWrite(t *testing.T) {
	tr := signedPauseTree(t)
	placedApart(t, "pause.json")
	if _, err := buildSigned(t, tr); err == nil || !strings.Contains(err.Error(), "pause.file: under the signed authority the plane's own account may not be able to change it") {
		t.Fatalf("build over a plane-writable pause file = %v", err)
	}
	placedApart(t)
	p, err := buildSigned(t, tr)
	if err != nil {
		t.Fatalf("build placed apart = %v", err)
	}
	snap := p.poller.Current()
	if snap.State() != pause.Clear {
		t.Fatalf("the pause state = %s, %q: %s", snap.State(), snap.Cause(), snap.Detail())
	}
	got := p.pauseAnswer(snap, time.Now()).Signed
	if got == nil || got.Subject != "operator-a" || got.Serial != 1 || got.Floor != 1 || got.KeyID != aliceSigner().KeyID {
		t.Errorf("the health page's signed pause = %+v", got)
	}
}

// TestASignedPlaneTakesOnlyAPermittedNewerPause: a pause by a key the roster
// does not name, and the replay of an older document, are unknown and
// block; a restart over the replay is refused by the floor.
func TestASignedPlaneTakesOnlyAPermittedNewerPause(t *testing.T) {
	tr := signedPauseTree(t)
	file := filepath.Join(tr.dir, "pause", "pause.json")
	placedApart(t)
	p, err := buildSigned(t, tr)
	if err != nil {
		t.Fatal(err)
	}
	older, err := os.ReadFile(file) //nolint:gosec // G304: a file under the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	global := pause.Entry{ID: "g", Scope: pause.Scope{Kind: pause.ScopeGlobal}, CreatedAt: time.Now().UTC()}
	if err := pause.AddSigned(t.Context(), file, global, aliceSigner()); err != nil {
		t.Fatal(err)
	}
	polled(t, p, "alice's pause", pause.Paused, "")
	mallory := signedTestKey("mallory")
	forged := pause.Signer{KeyID: policykey.KeyID(signedTestPublic("mallory")), Sign: func(payloadType string, body []byte) ([]byte, error) {
		return bundle.SignBytesAs(payloadType, body, mallory)
	}}
	if err := pause.RemoveSigned(t.Context(), file, "g", forged); err != nil {
		t.Fatal(err)
	}
	polled(t, p, "mallory's lift", pause.Unknown, pause.CauseUnauthenticated)
	writeTestFile(t, file, older)
	polled(t, p, "serial 1 replayed", pause.Unknown, pause.CauseBelowFloor)
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := buildSigned(t, tr); !errors.Is(err, pause.ErrNotServable) || !strings.Contains(err.Error(), string(pause.CauseBelowFloor)) {
		t.Errorf("a restart over serial 1 replayed = %v", err)
	}
}

// polled polls p's pause file and fails unless it reads as state with cause.
func polled(t *testing.T, p *plane, what string, state pause.State, cause pause.Cause) {
	t.Helper()
	if s := p.poller.Poll(); s.State() != state || s.Cause() != cause {
		t.Errorf("%s polled = %s, %q: %s; want %s, %q", what, s.State(), s.Cause(), s.Detail(), state, cause)
	}
}

// TestDoctorJudgesASignedPauseAsThePlaneWould: the signer's key, its grant
// and the scope, refused as the authenticator and the authorizer refuse.
func TestDoctorJudgesASignedPauseAsThePlaneWould(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	entry := func(name, level string) map[string]any {
		return map[string]any{
			"keyId": policykey.KeyID(signedTestPublic(name)), "alg": "ed25519", "role": "operator", "subject": name,
			"publicKey": policykey.FormatPublic(signedTestPublic(name)),
			"grants":    []any{map[string]any{"tenant": "acme", "project": "orders", "level": level}},
		}
	}
	body, err := canon.Canonicalize(map[string]any{
		"kind": trust.DocumentKind, "rosterId": "roster-a", "serial": 1,
		"issuedAt": now.Add(-time.Hour).Format(time.RFC3339), "expiresAt": now.Add(time.Hour).Format(time.RFC3339),
		"keys": []any{entry("alice", "operate"), entry("bob", "read")},
	})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := bundle.SignBytesAs(trust.PayloadType, body, signedTestKey("root"))
	if err != nil {
		t.Fatal(err)
	}
	roster, err := trust.VerifyDocument(trust.Envelope{PayloadType: trust.PayloadType, Payload: body,
		Signatures: []trust.Signature{{KeyID: policykey.KeyID(signedTestPublic("root")), Signature: sig}}}, signedTestPublic("root"))
	if err != nil {
		t.Fatal(err)
	}
	signedBy := func(signer, as, project string, d pause.Document) (policy.StatementEnvelope, pause.SignedDocument) {
		h := pause.Header{KeyID: policykey.KeyID(signedTestPublic(as)), Tenant: "acme", Project: project, Serial: 1}
		body, err := pause.MarshalSigned(h, d)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := bundle.SignBytesAs(operators.PausePayloadType, body, signedTestKey(signer))
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := pause.ParseSigned(body)
		if err != nil {
			t.Fatal(err)
		}
		return policy.StatementEnvelope{PayloadType: operators.PausePayloadType, Payload: body,
			Signatures: []policy.StatementSignature{{KeyID: h.KeyID, Signature: sig}}}, parsed
	}
	e, b := signedBy("alice", "alice", "orders", pause.Document{})
	if subject, err := pauseSigner(roster, e, b, "acme", "orders"); err != nil || subject != "alice" {
		t.Fatalf("alice's pause = %q, %v", subject, err)
	}
	for _, c := range []struct {
		name, signer, as, project string
		want                      pause.Error
	}{
		{"a pause of another project", "alice", "alice", "other", pause.ErrOtherScope},
		{"mallory under alice's key id", "mallory", "alice", "orders", pause.ErrUnauthenticated},
		{"a key the roster does not name", "mallory", "mallory", "orders", pause.ErrUnauthenticated},
		{"bob, who only reads orders", "bob", "bob", "orders", pause.ErrUnauthorized},
	} {
		e, b := signedBy(c.signer, c.as, c.project, pause.Document{})
		if _, err := pauseSigner(roster, e, b, "acme", "orders"); !errors.Is(err, c.want) {
			t.Errorf("%s = %v, want %v", c.name, err, c.want)
		}
	}
}

// TestTheHealthPageNamesThePausesSignerAndFloorOnly: the member carries the
// operator's subject and key id, the serial and the floor, and is left out
// where no signed document stands behind the state.
func TestTheHealthPageNamesThePausesSignerAndFloorOnly(t *testing.T) {
	raw, err := json.Marshal(pauseAnswer{UnlistedOnly: []string{}, Signed: signedAnswer(pause.SignedBy{Subject: "alice", KeyID: "ed25519:ab", Serial: 3, Floor: 3}, true)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"signed":{"subject":"alice","key_id":"ed25519:ab","serial":3,"floor":3}`; !strings.Contains(string(raw), want) {
		t.Errorf("the pause answer = %s, want %s", raw, want)
	}
	if raw, _ := json.Marshal(pauseAnswer{Signed: signedAnswer(pause.SignedBy{Subject: "alice"}, false)}); strings.Contains(string(raw), "signed") {
		t.Errorf("an unsigned state's answer = %s", raw)
	}
}
