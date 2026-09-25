// The codec's fuzz target. The Builder has its own in event_test.go; this one
// is about bytes nothing in this package wrote.
package evidence_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/guardana/control/internal/evidence"
)

// FuzzDecodeJSONL asserts three things over arbitrary bytes: the decoder either
// refuses or returns events, never both and never a panic; anything it does
// return survives being written out and read back; and what is written is one
// line per event for every reader, holding nothing the encoder escapes.
//
// The one accepted exception is named below. It is not a way out of the
// property: it is a real asymmetry between the two bounds, and
// TestReEncodingCanCrossTheLineBound holds it still.
func FuzzDecodeJSONL(f *testing.F) {
	const valid = `{"eventId":"evt-1","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"req-1"}`

	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte("{}\n"))
	f.Add([]byte(valid + "\n"))
	f.Add([]byte(valid + "\r\n" + valid + "\n"))
	f.Add([]byte(valid + "\n" + `{"eventId":"evt-2","kin`))
	f.Add([]byte(`{"eventId":"evt-1","kind":99}` + "\n"))
	f.Add([]byte(`{"eventId":"evt-1","kind":1,"occurredAt":"2026-09-09T12:00:00Z"}` + "\n"))
	f.Add([]byte(`{"eventId":"a","prevEventId":"b","decision":{"verdict":"VERDICT_DENY"}}` + "\n"))
	f.Add([]byte("null\n[]\n"))
	f.Add([]byte(strings.Repeat("a", 300)))
	// Raw runes the encoder escapes, inside a string: they decode, and they
	// have to leave as escapes. Built from their numbers, so this file holds no
	// character its reader cannot see.
	f.Add([]byte(`{"eventId":"a` + string(rune(0x2028)) + `b"}` + "\n"))
	f.Add([]byte(`{"eventId":"a` + string(rune(0x9b)) + `31m` + string(rune(0x202e)) + `"}` + "\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		const limit = 16

		events, err := evidence.DecodeJSONL(bytes.NewReader(data), limit)
		if err != nil {
			if events != nil {
				t.Fatalf("refused with %v and still returned %d events", err, len(events))
			}
			return
		}
		if len(events) > limit {
			t.Fatalf("returned %d events for a limit of %d", len(events), limit)
		}

		var buf bytes.Buffer
		switch err := evidence.EncodeJSONL(&buf, events); {
		case err == nil:
		case errors.Is(err, evidence.ErrLineTooLong):
			// A line at the bound can grow when it is written back: a declared
			// enum given as a number arrives as a number and leaves as a name,
			// and a rune the encoder escapes arrives raw and leaves as six
			// bytes. Both directions refuse loudly, which is the property that
			// matters; see TestReEncodingCanCrossTheLineBound.
			return
		default:
			t.Fatalf("EncodeJSONL of decoded events: %v", err)
		}

		// Whatever was read, what is written is one line per event for every
		// reader and holds nothing the encoder escapes.
		if problem := writtenLinesProblem(buf.Bytes(), len(events)); problem != "" {
			t.Fatal(problem)
		}

		again, err := evidence.DecodeJSONL(&buf, limit)
		if err != nil {
			t.Fatalf("DecodeJSONL of encoded events: %v", err)
		}
		if len(again) != len(events) {
			t.Fatalf("round trip returned %d events, want %d", len(again), len(events))
		}
		for i := range events {
			if !proto.Equal(events[i], again[i]) {
				t.Fatalf("event %d changed:\nbefore %v\nafter  %v", i, events[i], again[i])
			}
		}
	})
}
