package trust_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/trust"
)

// otherPayloadTypes is every other DSSE payload type a key of this project
// signs, spelled here rather than taken from the packages that own them.
var otherPayloadTypes = []string{
	"application/vnd.agent-policy+json",
	"application/vnd.agent-policy-freshness+json",
	"application/vnd.agent-reaction-route+json",
	"application/vnd.agent-reaction-lift+json",
	"application/vnd.agent-approval-answer+json",
}

func TestTheRosterPayloadTypeIsNoOtherTypes(t *testing.T) {
	if trust.PayloadType != "application/vnd.agent-trust-roster+json" {
		t.Fatalf("PayloadType = %q", trust.PayloadType)
	}
	for _, other := range otherPayloadTypes {
		if other == trust.PayloadType {
			t.Errorf("the roster shares its payload type with %q", other)
		}
	}
}

func TestAnEnvelopeOfAnotherPayloadTypeNeverVerifiesAsARoster(t *testing.T) {
	body := canonical(t, doc(1, "alice"))
	for _, other := range otherPayloadTypes {
		env := signBody(t, other, body, "root")
		if _, err := trust.VerifyDocument(env, public("root")); !errors.Is(err, trust.ErrPayloadType) {
			t.Errorf("an envelope of %s = %v, want ErrPayloadType", other, err)
		}
		env.PayloadType = trust.PayloadType
		if _, err := trust.VerifyDocument(env, public("root")); !errors.Is(err, trust.ErrSignature) {
			t.Errorf("a signature over %s relabelled as a roster = %v, want ErrSignature", other, err)
		}
	}
	if _, err := trust.VerifyDocument(signed(t, doc(1, "alice")), public("root")); err != nil {
		t.Fatalf("the control, the same body signed as a roster: %v", err)
	}
}

func TestARosterNeverVerifiesAsAnotherSignedFile(t *testing.T) {
	env := signed(t, doc(1, "alice"))
	keys := bundle.Keyring{idOf("root"): public("root")}
	if _, err := policy.VerifyStatement(env, keys); !errors.Is(err, policy.ErrStatementPayloadType) {
		t.Errorf("a roster read as a freshness statement = %v", err)
	}
	if _, err := reaction.VerifyRoute(env, public("root")); !errors.Is(err, reaction.ErrRoutePayloadType) {
		t.Errorf("a roster read as a route = %v", err)
	}
	if _, err := reaction.VerifyLift(env, public("root")); !errors.Is(err, reaction.ErrLiftPayloadType) {
		t.Errorf("a roster read as a lift = %v", err)
	}
	relabelled := env
	relabelled.PayloadType = bundle.StatementPayloadType
	if _, err := policy.VerifyStatement(relabelled, keys); !errors.Is(err, policy.ErrStatementSignature) {
		t.Errorf("a roster relabelled as a freshness statement = %v, want its signature refused", err)
	}
}

func TestARosterIsSignedByThePinnedRootOnly(t *testing.T) {
	body := canonical(t, doc(1, "alice"))
	stranger := signBody(t, trust.PayloadType, body, "stranger")
	if _, err := trust.VerifyDocument(stranger, public("root")); !errors.Is(err, trust.ErrRootKey) {
		t.Errorf("a roster signed by another key = %v, want ErrRootKey", err)
	}
	stranger.Signatures[0].KeyID = idOf("root")
	if _, err := trust.VerifyDocument(stranger, public("root")); !errors.Is(err, trust.ErrSignature) {
		t.Errorf("another key's signature under the root's key id = %v, want ErrSignature", err)
	}
	twice := signed(t, doc(1, "alice"))
	twice.Signatures = append(twice.Signatures, twice.Signatures[0])
	if _, err := trust.VerifyDocument(twice, public("root")); !errors.Is(err, trust.ErrSignatures) {
		t.Errorf("two signatures = %v, want ErrSignatures", err)
	}
	none := signed(t, doc(1, "alice"))
	none.Signatures = nil
	if _, err := trust.VerifyDocument(none, public("root")); !errors.Is(err, trust.ErrSignatures) {
		t.Errorf("no signature = %v, want ErrSignatures", err)
	}
}

func TestSignDocumentWritesTheCanonicalRosterAndItsDigest(t *testing.T) {
	source := `{"kind":"trust-roster/v1alpha1","rosterId":"roster-a","serial":4,"keys":[` +
		string(canonical(t, keyEntry("alice"))) + `]}`
	at := clockAt(t, issued)
	env, d, err := trust.SignDocument([]byte(source), at, trust.DefaultLifetime, private("root"))
	if err != nil {
		t.Fatalf("SignDocument: %v", err)
	}
	wantStart := `{"expiresAt":"2026-10-31T00:00:00Z","issuedAt":"2026-10-30T00:00:00Z","keys":[{"alg":"ed25519",` +
		`"grants":[{"level":"operate","project":"p1","tenant":"t1"}],"keyId":"ed25519-`
	wantEnd := `"role":"operator","subject":"alice"}],"kind":"trust-roster/v1alpha1","rosterId":"roster-a","serial":4}`
	if body := string(env.Payload); !strings.HasPrefix(body, wantStart) || !strings.HasSuffix(body, wantEnd) {
		t.Errorf("the body = %s", body)
	}
	sum := sha256.Sum256(env.Payload)
	if want := "sha256:" + hex.EncodeToString(sum[:]); d.Digest() != want {
		t.Errorf("Digest = %s, want %s", d.Digest(), want)
	}
	if d.ID() != "roster-a" || d.Serial() != 4 || !d.ExpiresAt().Equal(clockAt(t, expires)) || len(d.Keys()) != 1 {
		t.Errorf("the document = %s %d %s %d keys", d.ID(), d.Serial(), d.ExpiresAt(), len(d.Keys()))
	}
	wantAlicesKey(t, d.Keys()[0])
}

func wantAlicesKey(t *testing.T, k trust.Key) {
	t.Helper()
	if k.Subject() != "alice" || k.Role() != trust.RoleOperator || k.Alg() != trust.AlgEd25519 || k.RosterSerial() != 4 {
		t.Errorf("the key = %s %s %s %d", k.Subject(), k.Role(), k.Alg(), k.RosterSerial())
	}
	if g := k.Grants(); len(g) != 1 || g[0] != (trust.Grant{Tenant: "t1", Project: "p1", Level: trust.LevelOperate}) {
		t.Errorf("the grants = %+v", g)
	}
}

func TestSignDocumentRefusesWhatNoPlaneWouldTake(t *testing.T) {
	source := `{"kind":"trust-roster/v1alpha1","rosterId":"roster-a","serial":4,"keys":[` +
		string(canonical(t, keyEntry("alice"))) + `]}`
	at := clockAt(t, issued)
	for name, s := range map[string]string{
		"a source naming issuedAt":  strings.Replace(source, `"serial":4`, `"serial":4,"issuedAt":"`+issued+`"`, 1),
		"a source naming expiresAt": strings.Replace(source, `"serial":4`, `"serial":4,"expiresAt":"`+expires+`"`, 1),
	} {
		if _, _, err := trust.SignDocument([]byte(s), at, trust.DefaultLifetime, private("root")); !errors.Is(err, trust.ErrMalformed) {
			t.Errorf("%s = %v, want ErrMalformed", name, err)
		}
	}
	if _, _, err := trust.SignDocument([]byte(source), at, trust.MaxLifetime+time.Second, private("root")); !errors.Is(err, trust.ErrTimes) {
		t.Errorf("a lifetime past MaxLifetime = %v, want ErrTimes", err)
	}
	if _, _, err := trust.SignDocument([]byte(source), at, trust.MaxLifetime, private("root")); err != nil {
		t.Errorf("a lifetime of MaxLifetime: %v", err)
	}
	if _, _, err := trust.SignDocument([]byte(source), at.Add(time.Millisecond), trust.DefaultLifetime, private("root")); !errors.Is(err, trust.ErrTimes) {
		t.Errorf("an issuedAt with a fraction = %v, want ErrTimes", err)
	}
	if _, _, err := trust.SignDocument([]byte(source), at, trust.DefaultLifetime, private("alice")); !errors.Is(err, trust.ErrKeyReused) {
		t.Errorf("a roster signed by a key it names = %v, want ErrKeyReused", err)
	}
}

func TestARootThatIsAnotherAuthoritysKeyIsRefused(t *testing.T) {
	others := map[string]ed25519.PublicKey{
		"policy": public("policy"), "freshness": public("freshness"), "route": public("route"), "lift": public("lift"),
	}
	all := []ed25519.PublicKey{public("policy"), public("freshness"), public("route"), public("lift")}
	if err := trust.CheckRoot(public("root"), all...); err != nil {
		t.Fatalf("the control, a root apart from every other key: %v", err)
	}
	for name, k := range others {
		if err := trust.CheckRoot(k, all...); !errors.Is(err, trust.ErrKeyReused) {
			t.Errorf("a root that is the %s key = %v, want ErrKeyReused", name, err)
		}
		negated := append(ed25519.PublicKey(nil), k...)
		negated[31] ^= 0x80
		if err := trust.CheckRoot(negated, all...); !errors.Is(err, trust.ErrKeyReused) {
			t.Errorf("a root that is the %s key with bit 255 flipped = %v, want ErrKeyReused", name, err)
		}
	}
	for name, k := range map[string]ed25519.PublicKey{"a short key": public("root")[:31], "the identity": weakKey()} {
		if err := trust.CheckRoot(k); !errors.Is(err, trust.ErrRootKey) {
			t.Errorf("%s as the root = %v, want ErrRootKey", name, err)
		}
	}
}

// weakKey is the encoding of the identity point, a key of small order.
func weakKey() ed25519.PublicKey {
	k := make(ed25519.PublicKey, ed25519.PublicKeySize)
	k[0] = 1
	return k
}

func TestARosterKeyThatIsTheRootIsRefused(t *testing.T) {
	d := doc(1, "alice", "root")
	if _, err := trust.VerifyDocument(signed(t, d), public("root")); !errors.Is(err, trust.ErrKeyReused) {
		t.Errorf("a roster naming the root as an operator = %v, want ErrKeyReused", err)
	}
}

func TestUsableAtRefusesAFutureOrExpiredRoster(t *testing.T) {
	d, err := trust.VerifyDocument(signed(t, doc(1, "alice")), public("root"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   string
		want error
	}{
		{"2026-10-29T23:59:59Z", trust.ErrFuture},
		{issued, nil},
		{"2026-10-30T23:59:59Z", nil},
		{expires, trust.ErrExpired},
	}
	for _, c := range cases {
		if err := d.UsableAt(clockAt(t, c.at)); !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("UsableAt(%s) = %v, want %v", c.at, err, c.want)
		}
	}
}

func TestTheZeroAnswerAndTheZeroKeyGrantNothing(t *testing.T) {
	var a trust.Answer
	if a.State() != trust.StateUnavailable {
		t.Errorf("the zero answer's state = %v, want StateUnavailable", a.State())
	}
	k := a.Key()
	if k.Role() != "" || len(k.Grants()) != 0 || k.ID() != "" {
		t.Errorf("the zero answer's key = %s %s %v", k.ID(), k.Role(), k.Grants())
	}
	env := signBody(t, "application/vnd.agent-approval-answer+json", []byte(`{}`), "alice")
	if err := k.Verify(env.PayloadType, env.Payload, env.Signatures[0].Signature); err == nil {
		t.Error("the zero key verified a signature")
	}
	if a := trust.Unavailable(trust.CauseExpired); a.State() != trust.StateUnavailable || a.Key().ID() != "" {
		t.Errorf("Unavailable = %v %q", a.State(), a.Key().ID())
	}
}

func TestParseKindIsClosed(t *testing.T) {
	for _, s := range []string{"", "file"} {
		if k, err := trust.ParseKind(s); err != nil || k != trust.KindFile {
			t.Errorf("ParseKind(%q) = %q, %v", s, k, err)
		}
	}
	for _, s := range []string{"api", "File", "file ", "memory"} {
		if _, err := trust.ParseKind(s); !errors.Is(err, trust.ErrUnknownKind) {
			t.Errorf("ParseKind(%q) = %v, want ErrUnknownKind", s, err)
		}
	}
}
