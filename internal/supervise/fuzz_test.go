package supervise_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/supervise"
)

// FuzzReadProcedure feeds ReadProcedure arbitrary bytes. Beside never
// panicking, a refusal is ErrProcedure with no procedure, and an acceptance
// is within the size bound, has the SHA-256 of its RFC 8785 form as its
// digest, and reads from that form as the same procedure.
func FuzzReadProcedure(f *testing.F) {
	for _, doc := range []string{
		procJSON, canonicalProc, "", "{}", "[]", "null",
		`{"schema_version":"0.1","procedure_id":"x","version":"1","steps":[{"id":"a","tool":"t","upstream":"u",` +
			`"observed_as":[],"required":false}],"order":{},"allow":[],"rules":` + rulesJSON + `}`,
		`{"schema_version":"0.1","procedure_id":"x","version":"1","steps":[{"id":"a","tool":"t","upstream":"u",` +
			`"observed_as":[],"required":false}],"order":{"a":["a"]},"allow":[],"rules":` + rulesJSON + `}`,
	} {
		f.Add([]byte(doc))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := supervise.ReadProcedure(raw)
		if err != nil {
			if p != nil || !errors.Is(err, supervise.ErrProcedure) {
				t.Fatalf("a refusal = %v, %v", p, err)
			}
			return
		}
		checkAccepted(t, raw, p)
	})
}

func checkAccepted(t *testing.T, raw []byte, p *supervise.Procedure) {
	t.Helper()
	if len(raw) > supervise.MaxProcedureBytes {
		t.Fatalf("accepted %d bytes", len(raw))
	}
	form, err := canon.CanonicalizeJSON(raw)
	if err != nil {
		t.Fatalf("accepted what the canonical form refuses: %v", err)
	}
	sum := sha256.Sum256(form)
	if p.Digest() != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %s is not of the canonical form", p.Digest())
	}
	again, err := supervise.ReadProcedure(form)
	if err != nil || again.Digest() != p.Digest() || again.ID() != p.ID() ||
		again.Version() != p.Version() || !reflect.DeepEqual(again.Steps(), p.Steps()) {
		t.Fatalf("the canonical form reads otherwise: %v", err)
	}
}
