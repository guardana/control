package trust_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/trust"
)

func firstKey(d map[string]any) map[string]any { return d["keys"].([]any)[0].(map[string]any) }

func firstGrant(d map[string]any) map[string]any {
	return firstKey(d)["grants"].([]any)[0].(map[string]any)
}

// grants is n distinct grants over t1.
func grants(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"tenant": "t1", "project": fmt.Sprintf("p%d", i), "level": "read"}
	}
	return out
}

// TestARosterBodyIsHeldToItsFormat runs each refusal beside its control: the
// same body with the one change removed verifies.
func TestARosterBodyIsHeldToItsFormat(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]any)
		want   error
	}{
		{"an unknown member", func(d map[string]any) { d["note"] = "x" }, trust.ErrMalformed},
		{"no keys member", func(d map[string]any) { delete(d, "keys") }, trust.ErrMalformed},
		{"no expiresAt", func(d map[string]any) { delete(d, "expiresAt") }, trust.ErrMalformed},
		{"a later kind", func(d map[string]any) { d["kind"] = "trust-roster/v1alpha2" }, trust.ErrKind},
		{"a roster id in upper case", func(d map[string]any) { d["rosterId"] = "Roster-a" }, trust.ErrMalformed},
		{"a roster id past its bound", func(d map[string]any) { d["rosterId"] = strings.Repeat("r", trust.MaxRosterIDBytes+1) }, trust.ErrMalformed},
		{"serial 0", func(d map[string]any) { d["serial"] = 0 }, trust.ErrMalformed},
		{"an expiry at the issue", func(d map[string]any) { d["expiresAt"] = issued }, trust.ErrTimes},
		{"a lifetime a second past MaxLifetime", func(d map[string]any) { d["expiresAt"] = "2026-11-06T00:00:01Z" }, trust.ErrTimes},
		{"an issuedAt with a fraction", func(d map[string]any) { d["issuedAt"] = "2026-10-30T00:00:00.5Z" }, trust.ErrTimes},
		{"no key at all", func(d map[string]any) { d["keys"] = []any{} }, trust.ErrMalformed},
		{"an algorithm the key does not carry", func(d map[string]any) { firstKey(d)["alg"] = "ecdsa-p256" }, trust.ErrKeyEntry},
		{"a role this build lacks", func(d map[string]any) { firstKey(d)["role"] = "policy" }, trust.ErrKeyEntry},
		{"a key id of another key", func(d map[string]any) { firstKey(d)["keyId"] = idOf("bob") }, trust.ErrKeyEntry},
		{"a key id naming another algorithm", func(d map[string]any) {
			firstKey(d)["keyId"] = strings.Replace(idOf("alice"), "ed25519-", "p256-", 1)
		}, trust.ErrKeyEntry},
		{"a key of small order", func(d map[string]any) {
			firstKey(d)["publicKey"] = policykey.FormatPublic(weakKey())
			firstKey(d)["keyId"] = policykey.KeyID(weakKey())
		}, trust.ErrKeyEntry},
		{"a subject with a colon", func(d map[string]any) { firstKey(d)["subject"] = "alice:admin" }, trust.ErrKeyEntry},
		{"a subject past its bound", func(d map[string]any) { firstKey(d)["subject"] = strings.Repeat("a", trust.MaxSubjectBytes+1) }, trust.ErrKeyEntry},
		{"a subject outside ASCII", func(d map[string]any) { firstKey(d)["subject"] = "аlice" }, trust.ErrKeyEntry},
		{"an empty project", func(d map[string]any) { firstGrant(d)["project"] = "" }, trust.ErrGrant},
		{"a project that reads as a pattern", func(d map[string]any) { firstGrant(d)["project"] = "*" }, trust.ErrGrant},
		{"an empty tenant", func(d map[string]any) { firstGrant(d)["tenant"] = "" }, trust.ErrGrant},
		{"a level this build lacks", func(d map[string]any) { firstGrant(d)["level"] = "owner" }, trust.ErrGrant},
		{"a key with no grant", func(d map[string]any) { firstKey(d)["grants"] = []any{} }, trust.ErrMalformed},
		{"one project granted twice", func(d map[string]any) {
			firstKey(d)["grants"] = append(firstKey(d)["grants"].([]any), map[string]any{"tenant": "t1", "project": "p1", "level": "admin"})
		}, trust.ErrGrant},
		{"one key named twice", func(d map[string]any) { d["keys"] = append(d["keys"].([]any), keyEntry("alice")) }, trust.ErrKeyReused},
		{"a grant past MaxGrants", func(d map[string]any) { firstKey(d)["grants"] = grants(trust.MaxGrants + 1) }, trust.ErrTooMany},
	}
	for _, c := range cases {
		d := doc(1, "alice", "bob")
		c.change(d)
		if _, err := trust.VerifyDocument(signed(t, d), public("root")); !errors.Is(err, c.want) {
			t.Errorf("%s = %v, want %v", c.name, err, c.want)
		}
	}
	at := map[string]func(map[string]any){
		"MaxGrants grants":           func(d map[string]any) { firstKey(d)["grants"] = grants(trust.MaxGrants) },
		"a lifetime of MaxLifetime":  func(d map[string]any) { d["expiresAt"] = "2026-11-06T00:00:00Z" },
		"a subject at its bound":     func(d map[string]any) { firstKey(d)["subject"] = strings.Repeat("a", trust.MaxSubjectBytes) },
		"a roster id at its bound":   func(d map[string]any) { d["rosterId"] = strings.Repeat("r", trust.MaxRosterIDBytes) },
		"one project, two tenants":   func(d map[string]any) { firstGrant(d)["tenant"] = "t2"; d["keys"].([]any)[1] = keyEntry("bob") },
		"a subject of mail spelling": func(d map[string]any) { firstKey(d)["subject"] = "alice.b+ops@example.org" },
	}
	for name, change := range at {
		d := doc(1, "alice", "bob")
		change(d)
		if _, err := trust.VerifyDocument(signed(t, d), public("root")); err != nil {
			t.Errorf("the control %s: %v", name, err)
		}
	}
}

func TestARosterBodyThatIsNotItsCanonicalFormIsRefused(t *testing.T) {
	body := canonical(t, doc(1, "alice"))
	spaced := append([]byte(" "), body...)
	if _, err := trust.VerifyDocument(signBody(t, trust.PayloadType, spaced, "root"), public("root")); !errors.Is(err, trust.ErrNotCanonical) {
		t.Errorf("a body with a leading space = %v, want ErrNotCanonical", err)
	}
	repeated := []byte(strings.Replace(string(body), `{"expiresAt"`, `{"expiresAt":"`+expires+`","expiresAt"`, 1))
	if _, err := trust.VerifyDocument(signBody(t, trust.PayloadType, repeated, "root"), public("root")); !errors.Is(err, trust.ErrMalformed) {
		t.Errorf("a member named twice = %v, want ErrMalformed", err)
	}
}

func TestTheRosterBoundsAreReachable(t *testing.T) {
	names := make([]string, trust.MaxKeys+1)
	for i := range names {
		names[i] = fmt.Sprintf("op%d", i)
	}
	if _, err := trust.VerifyDocument(signed(t, doc(1, names[:trust.MaxKeys]...)), public("root")); err != nil {
		t.Errorf("MaxKeys keys: %v", err)
	}
	if _, err := trust.VerifyDocument(signed(t, doc(1, names...)), public("root")); !errors.Is(err, trust.ErrTooMany) {
		t.Errorf("MaxKeys+1 keys = %v, want ErrTooMany", err)
	}
	big := signed(t, doc(1, "alice"))
	big.Payload = append(big.Payload, make([]byte, trust.MaxBodyBytes+1-len(big.Payload))...)
	if _, err := trust.VerifyDocument(big, public("root")); !errors.Is(err, trust.ErrTooLarge) {
		t.Errorf("a body of MaxBodyBytes+1 = %v, want ErrTooLarge", err)
	}
	if _, err := trust.ParseFile(make([]byte, trust.MaxFileBytes+1)); err == nil {
		t.Error("ParseFile read a file past MaxFileBytes")
	}
}
