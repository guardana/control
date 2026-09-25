// The line bound against the largest proposal the contract accepts. An
// ACTION_PROPOSED event carries one whole envelope, so MaxLineBytes has to fit
// the worst case of what contract.Validate accepts, spelled in JSON and
// escaped, beside the event's own fields (docs/contracts.md, "Limits").
package evidence_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/pkg/contract"
)

// quotes is n quotation marks. A quote is the costliest byte an identifier may
// hold on a line: JSON spells it in two, and the identifier rule refuses every
// rune the encoder would spell in more.
func quotes(n int) string { return strings.Repeat(`"`, n) }

// marks is n bytes of U+061C, the Arabic letter mark. It is the costliest text
// the free-text rule admits: two bytes raw and six escaped, three times its
// size where a quote is twice. Built from its number, so this file holds none
// raw.
func marks(n int) string { return strings.Repeat(string(rune(0x061c)), n/2) }

// longest is the declared value of an enum, UNSPECIFIED aside, with the
// longest name, which is the one that costs a line the most. A tie goes to the
// lower number, so the choice does not depend on map order.
func longest[E ~int32](names map[int32]string) E {
	best, bestLen := int32(0), -1
	for number, name := range names {
		if number == 0 {
			continue
		}
		if len(name) > bestLen || (len(name) == bestLen && number < best) {
			best, bestLen = number, len(name)
		}
	}
	return E(best)
}

// largestProposal is an envelope contract.Validate accepts, exactly
// MaxEnvelopeBytes long, that spends its bytes where a line spends the most on
// them. Every string is at its limit and made of quotes, except the host: the
// host rule (ADR-0011) allows lower-case letters, digits, dots, colons and
// hyphens, none of which JSON escapes, so a host at its limit is the worst a
// host can cost. Both free-text fields are made of letter marks, every enum
// carries its longest name, and every timestamp has nanoseconds. What is left
// of the limit goes to whole identifiers in the lists, where an element costs
// its field name once rather than once per value. The maps stay empty,
// because a map entry costs a line less than two list elements holding the
// same bytes.
func largestProposal(t *testing.T) *controlv1.ActionEnvelope {
	t.Helper()
	id := quotes(contract.MaxStringBytes)
	// Where two values have to differ or chain, a two-digit prefix does it.
	numbered := func(i int) string { return fmt.Sprintf("%02d", i) + id[2:] }
	latest := timestamppb.New(time.Date(9999, 12, 31, 23, 59, 59, 999_999_999, time.UTC))

	env := &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     id,
		TraceId:       id,
		SpanId:        id,
		OccurredAt:    latest,
		ProjectId:     id,
		TenantId:      id,
		Environment:   id,
		Principal:     &controlv1.Principal{Id: numbered(0), Type: id, AuthnStrength: id, TenantId: id},
		Agent: &controlv1.Agent{
			Id: numbered(contract.MaxDelegationDepth), InstanceId: id, Framework: id, Version: id, ModelRef: id,
		},
		Action: &controlv1.Action{
			Kind: id, Name: id, Protocol: id, Provider: id,
			Effect: longest[controlv1.EffectClass](controlv1.EffectClass_name),
		},
		Resource: &controlv1.Resource{Type: id, Id: id, TenantId: id, Environment: id},
		Destination: &controlv1.Destination{
			TrustZone: longest[controlv1.TrustZone](controlv1.TrustZone_name),
			Host:      strings.Repeat("h", contract.MaxStringBytes),
		},
		Data: &controlv1.DataLabels{ContainsSecrets: true},
		Arguments: &controlv1.Arguments{
			RedactedPreview: marks(contract.MaxPreviewBytes), SchemaRef: id, RedactionProfile: id,
		},
		Context: &controlv1.RunContext{SessionId: id, RunId: id, StepId: id, Risk: id},
	}
	for range contract.MaxLabels {
		env.Data.Sensitivities = append(env.Data.Sensitivities,
			longest[controlv1.Sensitivity](controlv1.Sensitivity_name))
	}
	for i := range contract.MaxDelegationDepth {
		env.Delegation = append(env.Delegation, &controlv1.Delegation{
			From: numbered(i), To: numbered(i + 1), Reason: marks(contract.MaxStringBytes),
			IssuedAt: latest, ExpiresAt: latest,
		})
	}
	fill(t, env, id)
	return env
}

// fill spends what is left of MaxEnvelopeBytes on whole identifiers in the
// lists, one list after another, and cuts the last one so that the envelope
// ends exactly at the limit.
func fill(t *testing.T, env *controlv1.ActionEnvelope, id string) {
	t.Helper()
	lists := []*[]string{&env.Context.Tags, &env.Data.Sources}
	for _, hop := range env.GetDelegation() {
		lists = append(lists, &hop.Scopes)
	}
	for _, list := range lists {
		for len(*list) < contract.MaxLabels {
			*list = append(*list, id)
			over := proto.Size(env) - contract.MaxEnvelopeBytes
			if over <= 0 {
				continue
			}
			// Measured again after each cut, because a shorter string can have
			// a shorter length prefix.
			last := &(*list)[len(*list)-1]
			for tries := 0; over != 0 && tries < 4 && over < len(*last); tries++ {
				*last = id[:len(*last)-over]
				over = proto.Size(env) - contract.MaxEnvelopeBytes
			}
			if over != 0 {
				t.Fatalf("the envelope cannot be cut to exactly %d bytes; it is %d",
					contract.MaxEnvelopeBytes, proto.Size(env))
			}
			return
		}
	}
	t.Fatalf("every list is full at %d bytes, short of the limit, so this is not the largest proposal",
		proto.Size(env))
}

// MaxLineBytes fits the worst case of what the contract accepts. The
// largest proposal Validate accepts, in an event whose own strings are each at
// MaxStringBytes and made of quotes, is written as one line and read back
// unchanged. That line is more than twice MaxEnvelopeBytes long, which is why
// the factor is not two: under a bound of twice, a proposal the contract
// accepts could not be written into evidence.
func TestTheLargestProposalTheContractAcceptsFitsOnALine(t *testing.T) {
	env := largestProposal(t)
	if err := contract.Validate(env); err != nil {
		t.Fatalf("contract.Validate refuses the envelope, so it says nothing about one a receiver accepts: %v", err)
	}
	if size := proto.Size(env); size != contract.MaxEnvelopeBytes {
		t.Fatalf("the envelope is %d bytes, want exactly MaxEnvelopeBytes, %d", size, contract.MaxEnvelopeBytes)
	}
	id := quotes(contract.MaxStringBytes)
	event := &controlv1.Event{
		EventId:         id,
		Kind:            controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED,
		RequestId:       id,
		RunId:           id,
		ProjectId:       id,
		TenantId:        id,
		OccurredAt:      env.GetOccurredAt(),
		SchemaVersion:   evidence.SchemaVersion,
		EnforcementMode: longest[controlv1.EnforcementMode](controlv1.EnforcementMode_name),
		ExecutionId:     id,
		PrevEventId:     id,
		PrevEventDigest: id,
		Payload:         &controlv1.Event_Proposed{Proposed: env},
	}

	var buf bytes.Buffer
	if err := evidence.EncodeJSONL(&buf, []*controlv1.Event{event}); err != nil {
		t.Fatalf("EncodeJSONL: %v", err)
	}
	wire := buf.Bytes()
	line := len(wire) - 1
	// Every letter mark reached the line as an escape, so the line measured is
	// the escaped one: half a mark per free-text byte, in the preview and in
	// each hop's reason. The backslash is built from its number.
	escape := []byte(string(rune(0x5c)) + "u061c")
	want := (contract.MaxPreviewBytes + contract.MaxDelegationDepth*contract.MaxStringBytes) / 2
	if got := bytes.Count(wire, escape); got != want {
		t.Errorf("the line holds %d escaped letter marks, want %d", got, want)
	}
	if line <= 2*contract.MaxEnvelopeBytes {
		t.Errorf("the line is %d bytes, within twice MaxEnvelopeBytes (%d), so it is no longer the case the factor is sized for",
			line, 2*contract.MaxEnvelopeBytes)
	}

	decoded, err := evidence.DecodeJSONL(&buf, 1)
	if err != nil {
		t.Fatalf("DecodeJSONL of the line EncodeJSONL wrote: %v", err)
	}
	if !proto.Equal(event, decoded[0]) {
		t.Error("the event changed on its way through the file")
	}
	t.Logf("envelope %d bytes; line %d bytes, %.3f times MaxEnvelopeBytes; MaxLineBytes %d",
		proto.Size(env), line, float64(line)/contract.MaxEnvelopeBytes, evidence.MaxLineBytes)
}
