// The decoder's fuzz target. It is an internal test because the decoder is
// what has to hold, not the directory around it: an entry is read back after a
// crash, from a file that may have been cut off anywhere.
package holdjournal

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

// framedSeeds are whole files: an entry as this build writes one, and the ways
// the frame around a body can be wrong. A mutation of one of these almost
// never carries a matching checksum, so what it reaches is the framing.
func framedSeeds(t testing.TB) [][]byte {
	t.Helper()
	valid := seedBytes(t)

	trailing := append(bytes.Clone(valid), 0)
	// A plausible length, and a body one byte apart from its checksum: the
	// offset a scan would stop at is exactly the end of this entry.
	flipped := bytes.Clone(valid)
	flipped[len(flipped)-2] ^= 0x20
	// A length field that claims one byte more than the body carries.
	overlong := bytes.Clone(valid)
	binary.BigEndian.PutUint32(overlong[len(magic):], uint32(len(valid)-headerBytes+1)) //nolint:gosec // G115: a seed is a few hundred bytes

	return [][]byte{
		valid,
		bytes.Clone(valid)[:len(valid)-1],
		trailing,
		flipped,
		overlong,
		[]byte(magic),
		[]byte(strings.Repeat("A", headerBytes)),
		nil,
	}
}

// bodySeeds are entry bodies, without a frame. The target frames what it is
// given here, so a mutated body is checksummed as written and reaches the
// decoder instead of stopping at the frame.
func bodySeeds(t testing.TB) [][]byte {
	t.Helper()
	body := seedBytes(t)[headerBytes:]
	swap := func(old, with string) []byte {
		return bytes.Replace(body, []byte(old), []byte(with), 1)
	}

	return [][]byte{
		body,
		// The JSON fails at the offset the frame says the entry ends on.
		swap(`"expires":"2026-03-01T12:15:00Z"`, `"expires":"2026-03-01T12:15:0`),
		swap(`"last_event_id":"evt-1",`, ``),
		swap(`"state":"held"`, `"state":"closing"`),
		swap(`"request_id":"req-1"`, `"request_id":"../req-1"`),
		swap(`"binding"`, `"bindinx"`),
		[]byte(`{"schema_version":"2.0"}`),
		nil,
	}
}

// FuzzEntry holds the decoder to four things over any bytes at all: it does
// not crash, it hands back nothing at all when it refuses, what it accepts can
// name a file and stands in a state this build knows, and what it accepts
// survives a round trip through the encoder. The first argument says which
// layer the bytes are: a whole file, frame and all, or an entry body the
// target frames itself so arbitrary input reaches the decoder rather than
// being turned away by a checksum no mutation can correct.
func FuzzEntry(f *testing.F) {
	for _, seed := range framedSeeds(f) {
		f.Add(false, seed)
	}
	for _, seed := range bodySeeds(f) {
		f.Add(true, seed)
	}
	f.Fuzz(func(t *testing.T, isBody bool, data []byte) {
		if isBody {
			data = withBody(data)
		}
		entry, err := decodeEntry(data, DefaultMaxEntryBytes)
		if err != nil {
			if entry.Approval != nil || entry.State != StateUnknown || entry.IDs.RequestID != "" {
				t.Fatalf("a refused entry handed back %+v", entry)
			}
			return
		}
		holdAccepted(t, entry)
		roundTrip(t, entry)
	})
}

// holdAccepted holds what the decoder let through: it names a file this
// package may write, so no input can put an entry outside the directory or
// under another request's name, and it stands complete in a state this build
// knows.
func holdAccepted(t *testing.T, entry Entry) {
	t.Helper()
	name, err := encodeName(entry.IDs.RequestID)
	if err != nil {
		t.Fatalf("an accepted entry names %q: %v", entry.IDs.RequestID, err)
	}
	if back, ok := decodeName(name); !ok || back != entry.IDs.RequestID {
		t.Fatalf("an accepted entry's name reads back as %q (ok %v)", back, ok)
	}
	switch {
	case entry.State == StateUnknown:
		t.Fatal("an accepted entry stands in no state this build knows")
	case entry.Approval.GetRequestId() != entry.IDs.RequestID:
		t.Fatalf("an accepted entry's approval names %q", entry.Approval.GetRequestId())
	case entry.Expires.IsZero():
		t.Fatal("an accepted entry has no expiry")
	}
}

// roundTrip holds an accepted entry to encoding and decoding back to itself.
func roundTrip(t *testing.T, entry Entry) {
	t.Helper()
	again, err := encodeEntry(entry, DefaultMaxEntryBytes)
	if err != nil {
		t.Fatalf("an entry that decoded will not encode: %v", err)
	}
	back, err := decodeEntry(again, DefaultMaxEntryBytes)
	if err != nil {
		t.Fatalf("an entry this build wrote will not decode: %v", err)
	}
	if back.IDs != entry.IDs || back.State != entry.State || back.LastEventID != entry.LastEventID ||
		back.Binding != entry.Binding || !back.Expires.Equal(entry.Expires) ||
		!proto.Equal(back.Approval, entry.Approval) {
		t.Fatalf("the round trip changed the entry: %+v then %+v", entry, back)
	}
}
