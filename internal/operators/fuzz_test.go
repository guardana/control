package operators_test

import (
	"bytes"
	"testing"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/operators"
	"github.com/guardana/control/internal/policy/strictjson"
)

// FuzzStatementBody signs whatever body it is given with alice's key, so
// every input reaches the statement's reader: it never panics, and a body it
// authenticates is canonical and names alice's algorithm and key.
func FuzzStatementBody(f *testing.F) {
	f.Add(answer(f, "alice").Payload)
	f.Add([]byte(`{"alg":"ed25519"}`))
	f.Add([]byte(`[]`))
	w := newWorld(f, standard())
	a := authenticator(f, w.roster, operators.Bounds{})
	f.Fuzz(func(t *testing.T, body []byte) {
		got := a.Authenticate(t.Context(), operators.ActApprove, sign(t, operators.AnswerPayloadType, body, "alice", idOf("alice")))
		if got.State() != operators.AuthAuthenticated {
			return
		}
		again, err := canon.CanonicalizeJSON(body)
		if err != nil || !bytes.Equal(again, body) {
			t.Fatalf("an authenticated body is not canonical: %s", body)
		}
		members, err := strictjson.ReadObject(body)
		alg, _ := strictjson.String(members["alg"])
		id, _ := strictjson.String(members["keyId"])
		if err != nil || alg != "ed25519" || id != idOf("alice") || got.Subject() != "alice" {
			t.Fatalf("an authenticated body names %q %q for %q", alg, id, got.Subject())
		}
	})
}
