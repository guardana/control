package contract_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// TestAnAcceptedStringAtMostDoublesInJSON pins what the Limits section says
// about the JSON spelling. protojson escapes a C0 control, the quote and the
// backslash and nothing else, so once the string rules refuse C0 outside tab,
// line feed and carriage return in free text, an accepted string of n bytes is
// spelled in at most 2n+2; a C0 control would cost six.
func TestAnAcceptedStringAtMostDoublesInJSON(t *testing.T) {
	// The worst case, exactly: every byte a quote, which the rule accepts.
	quotes := strings.Repeat(`"`, contract.MaxStringBytes)
	if err := contract.CheckIdentifier(quotes); err != nil {
		t.Fatalf("a string of quotes is refused, so the worst case below is not one an envelope can carry: %v", err)
	}
	if got, err := jsonLen(quotes); err != nil || got != 2*len(quotes)+2 {
		t.Errorf("protojson spells %d quotes in %d bytes (%v), want exactly %d", len(quotes), got, err, 2*len(quotes)+2)
	}

	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.OneOf(
			rapid.String(),
			rapid.StringOf(rapid.SampledFrom([]rune{'"', '\\', '\t', '\n', '\r', 0x01, 0x1f, 0x7f, 0x85, 0x2028, 'a', 0xe9})),
		).Draw(rt, "s")
		preview := richEnvelope()
		preview.Arguments.RedactedPreview = s
		if contract.CheckIdentifier(s) != nil && contract.Validate(preview) != nil {
			return // refused as an identifier and as free text
		}
		got, err := jsonLen(s)
		if err != nil {
			rt.Fatalf("protojson could not spell the accepted string %+q: %v", s, err)
		}
		if got > 2*len(s)+2 {
			rt.Fatalf("protojson spells the accepted %d-byte string %+q in %d bytes, more than twice", len(s), s, got)
		}
	})
}

func jsonLen(s string) (int, error) {
	out, err := protojson.Marshal(wrapperspb.String(s))
	return len(out), err
}

// TestAnAcceptedEnvelopeCanOutgrowTheLimitInJSON records what the Limits
// section states: MaxEnvelopeBytes measures the binary encoding, and a message
// within it can have a JSON spelling past it. DecodeJSON refuses that
// spelling, and an evidence line bounded at MaxEnvelopeBytes of JSON cannot
// carry the proposal. It fails the day that stops being true, which is when
// the Limits section has to change.
func TestAnAcceptedEnvelopeCanOutgrowTheLimitInJSON(t *testing.T) {
	e := valid()
	quotes := strings.Repeat(`"`, contract.MaxStringBytes)
	key := func(i int) string { return fmt.Sprintf("%04d", i) + quotes[4:] }
	e.Principal.Attributes = make(map[string]string, contract.MaxLabels)
	e.Resource.Labels = make(map[string]string, contract.MaxLabels)
	e.Context = &controlv1.RunContext{}
	e.Data = &controlv1.DataLabels{}
	for i := range contract.MaxLabels {
		e.Principal.Attributes[key(i)] = quotes
		e.Resource.Labels[key(i)] = quotes
		e.Context.Tags = append(e.Context.Tags, quotes)
		e.Data.Sources = append(e.Data.Sources, quotes)
	}
	if err := contract.Validate(e); err != nil {
		t.Fatalf("the envelope is refused, so it says nothing about one a receiver accepts: %v", err)
	}
	binary := proto.Size(e)
	document, err := protojson.Marshal(e)
	if err != nil {
		t.Fatalf("protojson: %v", err)
	}
	if len(document) <= contract.MaxEnvelopeBytes {
		t.Errorf("an accepted envelope of %d bytes is %d bytes of JSON, within the limit: the Limits section says it can outgrow it",
			binary, len(document))
	}
	if _, err := contract.DecodeJSON(document); !errors.Is(err, contract.ErrTooLarge) {
		t.Errorf("DecodeJSON of the same message = %v, want ErrTooLarge: the two entry points measure different encodings", err)
	}
	t.Logf("binary %d bytes, JSON %d bytes, ratio %.2f; MaxEnvelopeBytes %d", binary, len(document),
		float64(len(document))/float64(binary), contract.MaxEnvelopeBytes)
}

// TestDecodeJSONBuildsWhatItParsesBeforeAnyBound pins the factor the Limits
// section states for the JSON path, which has no pre-scan: protojson builds
// every element before Validate counts them. Empty delegation hops are the
// costliest shape measured. It fails when the factor grows past what the page
// promises, and logs the number the page rounds.
func TestDecodeJSONBuildsWhatItParsesBeforeAnyBound(t *testing.T) {
	const ceiling = 80
	document := `{"delegation":[` + strings.Repeat(`{},`, (contract.MaxEnvelopeBytes-18)/3) + `{}]}`
	if len(document) > contract.MaxEnvelopeBytes {
		t.Fatalf("%d bytes is over MaxEnvelopeBytes, so the length check refuses it before the parse", len(document))
	}
	var err error
	spent := allocated(func() { _, err = contract.DecodeJSON([]byte(document)) })
	if err == nil {
		t.Fatal("the flood was accepted, so this measures something else")
	}
	factor := float64(spent) / float64(len(document))
	if factor > ceiling {
		t.Errorf("DecodeJSON allocated %d bytes for %d, %.1f times; the Limits section says under %d", spent, len(document), factor, ceiling)
	}
	t.Logf("DecodeJSON allocated %d bytes for %d bytes of empty delegation hops, %.1f times", spent, len(document), factor)
}
