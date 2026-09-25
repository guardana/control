package contract

import (
	"bytes"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// scanEntry reads one entry of the map fd before the runtime does, and returns
// the key the runtime will file it under. The runtime keeps an entry's key and
// value, each read at its own wire type, and drops anything else without a
// trace, so no walk after the parse can refuse it. It is refused here instead
// (ADR-0011), as an unknown field of the map: the walk names an entry by its
// position among the sorted keys, which is not known before the parse.
//
// An entry with no key is filed under the empty key, as the runtime files it.
// Every map v1 carries is keyed by string, which
// TestEveryMapTheEnvelopeCarriesIsKeyedByString pins, so a key is its bytes.
func scanEntry(fd protoreflect.FieldDescriptor, entry []byte, path string, depth int) ([]byte, error) {
	var key []byte
	var seen [2]bool // the entry's key and value, by index
	for len(entry) > 0 {
		num, typ, n := protowire.ConsumeTag(entry)
		if n < 0 {
			return nil, malformed(n)
		}
		v, size := consumeValue(num, typ, entry[n:])
		if size < 0 {
			return nil, malformed(size)
		}
		entry = entry[n+size:]
		field := fd.Message().Fields().ByNumber(num)
		switch {
		case field == nil:
			return nil, droppedFromEntry(path, num, "which holds only a key and a value")
		case typ != wireType(field.Kind()):
			return nil, droppedFromEntry(path, num, "on a wire type it is not read from")
		case seen[field.Index()]:
			return nil, sentTwice(path)
		}
		seen[field.Index()] = true
		if field.Number() == fd.MapKey().Number() {
			key = v.bytes
			continue
		}
		if err := scanValue(field, v, path, depth); err != nil {
			return nil, err
		}
	}
	return key, nil
}

// file records the key an entry is filed under and refuses one the map holds
// already (ADR-0011). The runtime keeps the last such entry, so a reader
// that kept the first would match a different value. Compared with every
// earlier key and not only the last, over at most MaxLabels of them.
func (st *fieldState) file(key []byte, path string) error {
	for _, k := range st.keys {
		if bytes.Equal(k, key) {
			return &ValidationError{Field: path, Err: errors.New("decode: two entries of one map hold one key")}
		}
	}
	st.keys = append(st.keys, key)
	return nil
}

// droppedFromEntry refuses a field the runtime would drop from a map entry. The
// number comes from the wire and the path from the schema, so no caller text
// reaches the refusal.
func droppedFromEntry(path string, num protowire.Number, why string) error {
	return &ValidationError{Field: path, Err: fmt.Errorf("%w: field %d of a map entry, %s", ErrUnknownField, num, why)}
}
