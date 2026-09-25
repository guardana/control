package contract_test

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/guardana/control/pkg/contract"
)

// callerText stands for anything a caller chose. A refusal is written into
// evidence, so none of it may come back in one (docs/contracts.md,
// "Refusals").
const callerText = "CALLER_TEXT_7F3A"

// maxRefusalBytes bounds a rendered codec refusal. The reason is this
// package's own sentence, so nothing a caller sends can lengthen it.
const maxRefusalBytes = 256

// TestDecodeJSONRefusalCarriesNoCallerText: protojson quotes its input back, a
// field name, an enum name or a whole value of any length. The rendering holds
// none of it; the codec's error, which does, stays reachable through Unwrap.
func TestDecodeJSONRefusalCarriesNoCallerText(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"an unknown field name", `{"schemaVersion":"1.0","` + callerText + `":1}`},
		{"an enum name", `{"schemaVersion":"1.0","action":{"effect":"` + callerText + `"}}`},
		{"a string where an int64 belongs", `{"schemaVersion":"1.0","context":{"budgets":{"k":"` + callerText + `"}}}`},
		{"a trust zone name", `{"schemaVersion":"1.0","destination":{"trustZone":"` + callerText + `"}}`},
		{"a timestamp", `{"schemaVersion":"1.0","occurredAt":"` + callerText + `"}`},
		{"a 200000-byte field name", `{"` + callerText + strings.Repeat("Q", 200000) + `":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := contract.DecodeJSON([]byte(tc.doc))
			if err == nil {
				t.Fatal("accepted, so this case proves nothing")
			}
			if strings.Contains(err.Error(), callerText) {
				t.Errorf("the refusal repeats the caller's text: %.200q", err.Error())
			}
			if n := len(err.Error()); n > maxRefusalBytes {
				t.Errorf("the refusal renders in %d bytes; nothing a caller sends may take it past %d", n, maxRefusalBytes)
			}
			if !chainNames(err, callerText) {
				t.Errorf("nothing in the chain of %v carries the codec's account of the input, so a caller that asks cannot reach it", err)
			}
		})
	}
}

// TestDecodeRendersOneReasonPerDecoder: the Protobuf runtime words its errors
// differently from one build to the next on purpose, so a refusal that quoted
// it would record the same bytes as two different reasons. Every codec refusal
// of one decoder renders the same.
func TestDecodeRendersOneReasonPerDecoder(t *testing.T) {
	invalidUTF8 := protowire.AppendString(protowire.AppendTag(nil, 2, protowire.BytesType), "req-\xff")
	truncated := []byte{0x12, 0x05, 'r'} // request_id, five bytes promised, one sent
	binary := [][]byte{{0xff, 0xff, 0xff, 0xff}, invalidUTF8, truncated}
	documents := []string{`{`, `[]`, `{"schemaVersion":1}`, `{"action":{"effect":"NOPE"}}`}

	assertOneReason(t, "Decode", len(binary), func(i int) error {
		_, err := contract.Decode(binary[i])
		return err
	})
	assertOneReason(t, "DecodeJSON", len(documents), func(i int) error {
		_, err := contract.DecodeJSON([]byte(documents[i]))
		return err
	})
}

func assertOneReason(t *testing.T, decoder string, n int, refuse func(int) error) {
	t.Helper()
	var first string
	for i := range n {
		err := refuse(i)
		if err == nil {
			t.Fatalf("%s accepted case %d, so it proves nothing", decoder, i)
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Errorf("%s renders two codec refusals differently: %q and %q", decoder, first, err.Error())
		}
	}
}

// chainNames reports whether any error in err's chain says text in its own
// message.
func chainNames(err error, text string) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if strings.Contains(err.Error(), text) {
			return true
		}
	}
	return false
}
