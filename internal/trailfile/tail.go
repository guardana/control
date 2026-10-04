package trailfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// eventMessage is the message every line the codec writes is.
var eventMessage = (&controlv1.Event{}).ProtoReflect().Descriptor()

// torn reports whether tail can be what a crash left of a line the codec
// wrote. It can only when it holds no white space outside a string, as the
// codec compacts its lines. Anything short of a whole JSON value can then only
// when it is a proper prefix of one JSON object whose members are, as far as
// the bytes go, the event's: each a field of its message under its JSON name,
// once, one member of a oneof at most, its value of the JSON type protojson
// gives the field. A whole value is the line cut between the object and its
// newline: its members are held to the same, and it decodes as one event.
func torn(tail []byte) bool {
	root := slot{msg: eventMessage}
	if !compact(tail) {
		return false
	}
	if !json.Valid(tail) {
		return walk(tail, root)
	}
	// A compact whole value ends on its closing byte, so the bytes before it
	// are a proper prefix the walk judges member by member.
	if !walk(tail[:len(tail)-1], root) {
		return false
	}
	events, err := evidence.DecodeJSONL(bytes.NewReader(tail), 1)
	return err == nil && len(events) == 1
}

// compact reports whether b holds no white space outside its strings.
func compact(b []byte) bool {
	inString, escaped := false, false
	for _, c := range b {
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			return false
		}
	}
	return true
}

// slot is what the next value may be. With msg set it is that message; with
// fd set, that field's value, or with elem one element of a list field or one
// value of a map field; with neither, anything.
type slot struct {
	msg  protoreflect.MessageDescriptor
	fd   protoreflect.FieldDescriptor
	elem bool
}

// level is one object or array the walk is inside. An object with msg set
// takes that message's fields as its members; one without takes any key, and
// its values are each, as an array's elements are.
type level struct {
	obj  bool
	msg  protoreflect.MessageDescriptor
	each slot
	// key is an object waiting for a member's name, and next the value that
	// follows the name read last.
	key  bool
	next slot
	seen map[string]bool
}

// walk follows b token by token from a value root may be, and reports whether
// b ends inside that value with every token, and the cut one after them as
// far as it goes, one the slot it stands in allows.
func walk(b []byte, root slot) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	var stack []level
	for {
		tok, err := dec.Token()
		switch {
		case errors.Is(err, io.ErrUnexpectedEOF):
			return fitsCut(stack, root, cutAfter(b, dec.InputOffset()))
		case errors.Is(err, io.EOF):
			return len(stack) > 0 && fitsCut(stack, root, cutAfter(b, dec.InputOffset()))
		case err != nil:
			return false
		}
		var ok bool
		if stack, ok = step(stack, root, tok); !ok || len(stack) == 0 {
			return false
		}
	}
}

// step takes one token, and returns the frames it leaves open and whether the
// token is one the codec could have written there.
func step(stack []level, root slot, tok json.Token) ([]level, bool) {
	d, delim := tok.(json.Delim)
	if delim && (d == '}' || d == ']') {
		return stack[:len(stack)-1], true
	}
	n := len(stack)
	if n > 0 && stack[n-1].key {
		name, _ := tok.(string)
		return stack, stack[n-1].member(name)
	}
	s := slotAt(stack, root)
	if !allows(s, firstByte(tok)) {
		return stack, false
	}
	if n > 0 && stack[n-1].obj {
		stack[n-1].key = true
	}
	if delim {
		stack = append(stack, enter(s, d))
	}
	return stack, true
}

// slotAt is the slot of the next value.
func slotAt(stack []level, root slot) slot {
	if len(stack) == 0 {
		return root
	}
	top := stack[len(stack)-1]
	if top.obj {
		return top.next
	}
	return top.each
}

// enter is the level a value in s opens with d.
func enter(s slot, d json.Delim) level {
	f := level{obj: d == '{', key: d == '{', seen: map[string]bool{}}
	switch {
	case s.msg != nil:
		f.msg = checked(s.msg)
	case s.fd == nil:
	case d == '[' || (!s.elem && s.fd.IsMap()):
		f.each = slot{fd: s.fd, elem: true}
	default:
		f.msg = checked(single(s.fd).Message())
	}
	return f
}

// member takes the member name, and reports whether the codec writes it here.
func (f *level) member(name string) bool {
	f.key = false
	if f.msg == nil {
		f.next = f.each
		return f.claim(name)
	}
	fd := f.msg.Fields().ByJSONName(name)
	if fd == nil {
		return false
	}
	f.next = slot{fd: fd}
	return f.claim(claimOf(fd))
}

// claim records name as written in this object, and reports whether it was
// not already.
func (f *level) claim(name string) bool {
	if f.seen[name] {
		return false
	}
	f.seen[name] = true
	return true
}

// claimOf is what a field takes in its object: its name, or its oneof's,
// which holds one member at most.
func claimOf(fd protoreflect.FieldDescriptor) string {
	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		return string(od.FullName())
	}
	return fd.JSONName()
}

// cutAfter is what b holds after the token that ends at offset, without the
// separator the decoder leaves unread when the bytes end.
func cutAfter(b []byte, offset int64) []byte {
	cut := b[offset:]
	if len(cut) > 0 && (cut[0] == ',' || cut[0] == ':') {
		return cut[1:]
	}
	return cut
}

// fitsCut reports whether cut, what follows the last whole token, can begin
// what the walk waits for: a member's name of the object it is in, or a value
// of the slot it stands in.
func fitsCut(stack []level, root slot, cut []byte) bool {
	if len(cut) == 0 {
		return true
	}
	n := len(stack)
	if n == 0 || !stack[n-1].key {
		return allows(slotAt(stack, root), cut[0])
	}
	top := stack[n-1]
	if cut[0] != '"' || top.msg == nil {
		return cut[0] == '"'
	}
	fields := top.msg.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if strings.HasPrefix(fd.JSONName(), string(cut[1:])) && !top.seen[claimOf(fd)] {
			return true
		}
	}
	return false
}

// firstByte is the byte a token's JSON begins with; any digit stands for a
// number.
func firstByte(tok json.Token) byte {
	switch t := tok.(type) {
	case json.Delim:
		return t.String()[0]
	case string:
		return '"'
	case bool:
		if t {
			return 't'
		}
		return 'f'
	case nil:
		return 'n'
	}
	return '0'
}

// allows reports whether a value in s can begin with c.
func allows(s slot, c byte) bool {
	first := starts(s)
	return first == "" || strings.IndexByte(first, c) >= 0
}

const number = "-0123456789"

// starts is the bytes a value in s begins with as protojson writes it, empty
// for any.
func starts(s slot) string {
	switch {
	case s.msg != nil:
		return messageStarts(s.msg)
	case s.fd == nil:
		return ""
	case !s.elem && s.fd.IsList():
		return "["
	case !s.elem && s.fd.IsMap():
		return "{"
	}
	fd := single(s.fd)
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return messageStarts(fd.Message())
	case protoreflect.BoolKind:
		return "tf"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return number
	case protoreflect.EnumKind, protoreflect.FloatKind, protoreflect.DoubleKind:
		return `"` + number
	}
	return `"`
}

// single is the descriptor of one value of fd: a map's value, or fd itself.
func single(fd protoreflect.FieldDescriptor) protoreflect.FieldDescriptor {
	if fd.IsMap() {
		return fd.MapValue()
	}
	return fd
}

// messageStarts is the bytes md's JSON begins with: a string for a time, any
// for another well-known type, an object for the rest.
func messageStarts(md protoreflect.MessageDescriptor) string {
	switch {
	case md.FullName() == "google.protobuf.Timestamp" || md.FullName() == "google.protobuf.Duration":
		return `"`
	case checked(md) == nil:
		return ""
	}
	return "{"
}

// checked is md when its members are walked as its fields, nil for a
// well-known type, whose JSON is its own.
func checked(md protoreflect.MessageDescriptor) protoreflect.MessageDescriptor {
	if md.ParentFile().Package() == "google.protobuf" {
		return nil
	}
	return md
}
