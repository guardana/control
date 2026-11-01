package trust_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/trust"
)

// FuzzRosterBody signs whatever body it is given with the root, so every
// input reaches the body's reader: it never panics, and a body it accepts is
// its own canonical form and has the digest of its bytes.
func FuzzRosterBody(f *testing.F) {
	f.Add(canonical(f, doc(1, "alice", "bob")))
	f.Add(canonical(f, doc(9007199254740991, "alice")))
	f.Add([]byte(`{"kind":"trust-roster/v1alpha1"}`))
	f.Add([]byte(`{"kind":"trust-roster/v1alpha2","keys":[]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		d, err := trust.VerifyDocument(signBody(t, trust.PayloadType, body, "root"), public("root"))
		if err != nil {
			return
		}
		again, err := canon.CanonicalizeJSON(body)
		if err != nil || !bytes.Equal(again, body) {
			t.Fatalf("an accepted body is not its canonical form: %s", body)
		}
		sum := sha256.Sum256(body)
		if d.Digest() != "sha256:"+hex.EncodeToString(sum[:]) || len(d.Keys()) == 0 {
			t.Fatalf("an accepted body reads as %s with %d keys", d.Digest(), len(d.Keys()))
		}
	})
}

// FuzzRosterFile holds the file reader to never panicking, and to reading
// back what it writes.
func FuzzRosterFile(f *testing.F) {
	raw, err := trust.MarshalFile(signed(f, doc(1, "alice")))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte(`{"payloadType":"x","payload":"","signatures":[]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		env, err := trust.ParseFile(raw)
		if err != nil {
			return
		}
		written, err := trust.MarshalFile(env)
		if err != nil {
			t.Fatalf("the writer refused what the reader accepted: %v", err)
		}
		back, err := trust.ParseFile(written)
		if err != nil || back.PayloadType != env.PayloadType || !bytes.Equal(back.Payload, env.Payload) {
			t.Fatalf("%s read back otherwise: %v", written, err)
		}
		if d, err := trust.VerifyDocument(env, public("root")); err == nil && d.ID() == "" {
			t.Fatal("a roster verified with no id")
		}
	})
}
