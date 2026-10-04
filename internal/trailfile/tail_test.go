package trailfile

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestATailIsCutOnlyWhenTheCodecCouldHaveWrittenIt: a cut line is judged
// member by member against the event as far as its bytes go. A member the
// event does not have, one written twice, a second member of one oneof, a
// value of another JSON type than the field's, and white space outside a
// string are each something the codec never writes, so the tail is refused
// and left; each sits beside a tail of the same shape that is cut.
func TestATailIsCutOnlyWhenTheCodecCouldHaveWrittenIt(t *testing.T) {
	for _, c := range []struct {
		tail string
		cut  bool
	}{
		{`{"eventId":1`, false},
		{`{"eventId":"1`, true},
		{`{"eventId":null`, false},
		{`{"eventId":"e2","eventId":"e`, false},
		{`{"eventId":"e2","prevEventId":"e`, true},
		{`{"eventId":"e2","eventI`, false},
		{`{"eventId":"e2","prev`, true},
		{`{"eventId":"e2","nope":`, false},
		{`{"eventId":"e2","nop`, false},
		{`{"eventId":"e2", "kind"`, false},
		{`{"eventId":"e2 x","kind"`, true},
		{`{"kind":[`, false},
		{`{"kind":"EVENT_KIND_`, true},
		{`{"occurredAt":{`, false},
		{`{"occurredAt":"2026-`, true},
		{`{"proposed":{},"decision":{`, false},
		{`{"proposed":{},"prevEventId":"`, true},
		{`{"decision":{"nosuch":`, false},
		{`{"decision":{"decisionLatencyUs":12`, false},
		{`{"decision":{"decisionLatencyUs":"12`, true},
		{`{"decision":{"reasonCodes":[1`, false},
		{`{"decision":{"reasonCodes":["A",`, true},
		{`{"decision":{"obligations":{`, false},
		{`{"decision":{"obligations":[{"advisory":"t`, false},
		{`{"decision":{"obligations":[{"advisory":tr`, true},
		{`{"decision":{"obligations":[{"params":{"k":1`, false},
		{`{"decision":{"obligations":[{"params":{"k":"v","k":"`, false},
		{`{"decision":{"obligations":[{"params":{"k":"v","j":"`, true},
	} {
		for _, prefix := range []string{"", lines(t, event(1))} {
			opensWithTail(t, prefix, c.tail, c.cut)
		}
	}
}

// TestEveryCutOfAnEventWithEveryFieldSetIsCut: an event with every field of
// its message and of each message under it set, one payload at a time, is
// cut at open wherever a crash stopped its line, so the walk refuses nothing
// the codec writes.
func TestEveryCutOfAnEventWithEveryFieldSetIsCut(t *testing.T) {
	payload := eventMessage.Oneofs().ByName("payload").Fields()
	for i := range payload.Len() {
		ev := &controlv1.Event{}
		fill(ev.ProtoReflect(), 0, payload.Get(i))
		line := strings.TrimSuffix(lines(t, ev), "\n")
		if !strings.Contains(line, `"`+payload.Get(i).JSONName()+`":{`) {
			t.Fatalf("the line %q does not hold its payload, so it does not test it", line)
		}
		for cut := 1; cut < len(line); cut++ {
			if !torn([]byte(line[:cut])) {
				t.Errorf("%s: the cut %q of a line the codec wrote is refused", payload.Get(i).Name(), line[:cut])
				break
			}
		}
		if !torn([]byte(line)) {
			t.Errorf("%s: the whole line is refused", payload.Get(i).Name())
		}
	}
}

// fill sets every field of m, recursing into messages to a depth of three,
// with the first member of each oneof but the event's payload, where only
// member is set.
func fill(m protoreflect.Message, depth int, member protoreflect.FieldDescriptor) {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() && fd != member && (member != nil || od.Fields().Get(0) != fd) {
			continue
		}
		switch {
		case fd.IsList():
			l := m.Mutable(fd).List()
			l.Append(sampleValue(l.NewElement(), fd, depth))
		case fd.IsMap():
			mp := m.Mutable(fd).Map()
			mp.Set(protoreflect.ValueOfString("k").MapKey(), sampleValue(mp.NewValue(), fd.MapValue(), depth))
		case fd.Kind() == protoreflect.MessageKind:
			if depth < 3 {
				fill(m.Mutable(fd).Message(), depth+1, nil)
			}
		default:
			m.Set(fd, sampleValue(protoreflect.Value{}, fd, depth))
		}
	}
}

// sampleValue is a value of fd's kind that is not its zero; blank is a new
// message to fill when fd holds messages.
func sampleValue(blank protoreflect.Value, fd protoreflect.FieldDescriptor, depth int) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		if depth < 3 {
			fill(blank.Message(), depth+1, nil)
		}
		return blank
	case protoreflect.StringKind:
		return protoreflect.ValueOfString("s \"q\" ż")
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes([]byte{1, 2})
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(true)
	case protoreflect.EnumKind:
		return protoreflect.ValueOfEnum(1)
	case protoreflect.Int64Kind:
		return protoreflect.ValueOfInt64(-7)
	case protoreflect.Uint32Kind:
		return protoreflect.ValueOfUint32(7)
	case protoreflect.Int32Kind:
		return protoreflect.ValueOfInt32(7)
	}
	return protoreflect.ValueOf(nil)
}
