package canon_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/pkg/contract"
)

// TestRefusesUnknownFields: a field this build does not know, in a message the
// digest reads, may be a field of the set that a later minor version added.
// Digesting without it would give two actions that differ only there one
// digest, so the digest refuses instead of relying on every caller to have
// validated the same object first.
func TestRefusesUnknownFields(t *testing.T) {
	// Field 99, varint 1: a number none of these messages declares.
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	f := loadFixture(t, "delegated")
	base := envelopeOf(t, f, "delegated")
	if len(base.GetDelegation()) != 2 {
		t.Fatalf("the delegated fixture has %d hops, want 2", len(base.GetDelegation()))
	}

	cases := map[string]func(*controlv1.ActionEnvelope) proto.Message{
		"":              func(e *controlv1.ActionEnvelope) proto.Message { return e },
		"/principal":    func(e *controlv1.ActionEnvelope) proto.Message { return e.GetPrincipal() },
		"/agent":        func(e *controlv1.ActionEnvelope) proto.Message { return e.GetAgent() },
		"/delegation/0": func(e *controlv1.ActionEnvelope) proto.Message { return e.GetDelegation()[0] },
		"/delegation/1": func(e *controlv1.ActionEnvelope) proto.Message { return e.GetDelegation()[1] },
		"/action":       func(e *controlv1.ActionEnvelope) proto.Message { return e.GetAction() },
		"/resource":     func(e *controlv1.ActionEnvelope) proto.Message { return e.GetResource() },
		"/destination":  func(e *controlv1.ActionEnvelope) proto.Message { return e.GetDestination() },
	}
	for pointer, pick := range cases {
		t.Run(pointer, func(t *testing.T) {
			env := proto.Clone(base).(*controlv1.ActionEnvelope)
			pick(env).ProtoReflect().SetUnknown(unknown)
			if len(pick(env).ProtoReflect().GetUnknown()) == 0 {
				t.Fatal("the unknown field did not land, so nothing is under test")
			}
			// The refusal narrows nothing Validate accepts.
			if err := contract.Validate(env); !errors.Is(err, contract.ErrUnknownField) {
				t.Errorf("Validate: err = %v, want ErrUnknownField", err)
			}

			_, digestErr := canon.DigestV1(env, f.AuthorizedArgs)
			_, actionErr := canon.CanonicalAction(env, f.AuthorizedArgs)
			for entry, err := range map[string]error{"DigestV1": digestErr, "CanonicalAction": actionErr} {
				if !errors.Is(err, canon.ErrUnsupportedValue) {
					t.Errorf("%s: err = %v, want ErrUnsupportedValue", entry, err)
					continue
				}
				if want := fmt.Sprintf("at %q", pointer); !strings.Contains(err.Error(), want) ||
					!strings.Contains(err.Error(), "unknown") {
					t.Errorf("%s: refusal %q, want it %s and to say the field is unknown", entry, err, want)
				}
			}
		})
	}
}
