package policykey_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policykey"
)

// FuzzParseStatement: for any bytes, ParseStatement returns the envelope's
// parts or exactly one of its refusals, never panics, and accepts only a file
// within the bound whose parts MarshalStatement writes back to a file that
// reads as the same parts.
func FuzzParseStatement(f *testing.F) {
	f.Add([]byte(envelopeFile))
	f.Add([]byte(envelopeFile + strings.Repeat(" ", 4096-len(envelopeFile))))
	f.Add([]byte(strings.Replace(envelopeFile, `"payload":"e30=",`, `"payload":"e30=","payload":"e30=",`, 1)))
	f.Add([]byte(strings.Replace(envelopeFile, `"e30="`, `"-_8="`, 1)))
	f.Add([]byte(strings.Replace(envelopeFile, `"f1"`, `"f\ud8001"`, 1)))
	f.Add([]byte(`[]`))
	grown := strings.Replace(envelopeFile, `"f1"`, `"`+strings.Repeat("\u2028", 1300)+`"`, 1)
	f.Add([]byte(grown + strings.Repeat(" ", max(0, 4096-len(grown)))))
	f.Fuzz(func(t *testing.T, raw []byte) {
		held := bytes.Clone(raw)
		env, err := policykey.ParseStatement(raw)
		if !bytes.Equal(raw, held) {
			t.Fatal("ParseStatement changed its input")
		}
		if err != nil {
			matched := 0
			for _, s := range statementFileRefusals() {
				if errors.Is(err, s) {
					matched++
				}
			}
			if matched != 1 {
				t.Fatalf("refusal %v matches %d sentinels", err, matched)
			}
			return
		}
		if len(raw) > 4096 {
			t.Fatalf("accepted %d bytes", len(raw))
		}
		again, err := policykey.MarshalStatement(env)
		if errors.Is(err, policykey.ErrStatementFileTooLarge) {
			// A file that spelled a character in fewer bytes than the
			// encoder does, U+2028 among them, can grow past the bound.
			return
		}
		if err != nil {
			t.Fatalf("MarshalStatement of accepted parts: %v", err)
		}
		back, err := policykey.ParseStatement(again)
		if err != nil || !sameParts(back, env) {
			t.Fatalf("the parts do not read back: %v", err)
		}
	})
}

// sameParts compares two envelopes' parts, bytes compared with bytes.Equal,
// which takes nil and empty as one, as base64 does.
func sameParts(a, b policy.StatementEnvelope) bool {
	if a.PayloadType != b.PayloadType || !bytes.Equal(a.Payload, b.Payload) || len(a.Signatures) != len(b.Signatures) {
		return false
	}
	for i := range a.Signatures {
		if a.Signatures[i].KeyID != b.Signatures[i].KeyID || !bytes.Equal(a.Signatures[i].Signature, b.Signatures[i].Signature) {
			return false
		}
	}
	return true
}
