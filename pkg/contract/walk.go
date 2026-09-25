package contract

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// walk visits every populated field of every message, in field-number order:
// Message.Range covers the same set in an unspecified order, and the first
// failure has to be reproducible. Generic on purpose, so a message added to the
// contract later is walked without anyone writing a visitor for it.
//
// depth counts descents below the root, which is walked at 0, so MaxNesting of
// them are walked and the next is refused. The bound is on the walk, not the
// schema: in a recursive tree the recursion and the path it builds are both
// unbounded, and nothing says a later minor adds no recursive message.
func walk(m protoreflect.Message, path string, depth int) error {
	if depth > MaxNesting {
		return &ValidationError{Field: path, Err: fmt.Errorf("%w: nested deeper than %d", ErrTooLarge, MaxNesting)}
	}
	if unknown := m.GetUnknown(); len(unknown) > 0 {
		return &ValidationError{Field: unknownFieldPath(path, unknown), Err: ErrUnknownField}
	}
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}
		if err := walkField(m.Get(fd), fd, join(path, string(fd.Name())), depth); err != nil {
			return err
		}
	}
	return nil
}

func walkField(v protoreflect.Value, fd protoreflect.FieldDescriptor, path string, depth int) error {
	switch {
	case fd.IsMap():
		return walkMap(v.Map(), fd, path, depth)
	case fd.IsList():
		list := v.List()
		if err := bound(list.Len(), MaxLabels, path, "entries"); err != nil {
			return err
		}
		for i := range list.Len() {
			if err := walkValue(list.Get(i), fd, path+"["+strconv.Itoa(i)+"]", depth); err != nil {
				return err
			}
		}
		return nil
	default:
		return walkValue(v, fd, path, depth)
	}
}

// walkMap visits entries in sorted key order and names them by position: a key
// is caller data, and a path carrying one puts caller text into every record.
func walkMap(m protoreflect.Map, fd protoreflect.FieldDescriptor, path string, depth int) error {
	if err := bound(m.Len(), MaxLabels, path, "entries"); err != nil {
		return err
	}
	keys := make([]protoreflect.MapKey, 0, m.Len())
	m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	stringKeys := fd.MapKey().Kind() == protoreflect.StringKind
	for i, key := range keys {
		entry := path + "[" + strconv.Itoa(i) + "]"
		if stringKeys {
			if err := checkKey(keys, i, entry); err != nil {
				return err
			}
		}
		if err := walkValue(m.Get(key), fd.MapValue(), entry, depth); err != nil {
			return err
		}
	}
	return nil
}

// checkKey holds the string key at keys[i] to the limit, to CheckMapKey's
// rule, then to the one rule ADR-0011 sets between keys. A key of any other
// kind is a number, which none of them reaches.
func checkKey(keys []protoreflect.MapKey, i int, entry string) error {
	s := keys[i].String()
	if err := bound(len(s), MaxStringBytes, entry+".key", "bytes"); err != nil {
		return err
	}
	if err := mapKeyProblem(s); err != nil {
		// The namespace refusal names the entry, a refusal of the key's
		// spelling the key.
		field := entry + ".key"
		if errors.Is(err, errReservedKey) {
			field = entry
		}
		return &ValidationError{Field: field, Err: err}
	}
	// A consumer that folds case, as encoding/json does for U+017F and U+212A
	// as well as ASCII, reads two such keys as one. Against every earlier key
	// and not only the neighbour: in byte order "Team" and "team" can have
	// "other" between them. Quadratic, over at most MaxLabels keys.
	for j := range i {
		if strings.EqualFold(keys[j].String(), s) {
			return &ValidationError{Field: entry + ".key", Err: fmt.Errorf("%w: folds together with the key at [%d]", ErrInvalidValue, j)}
		}
	}
	return nil
}

// walkValue handles one scalar, one list element or one map value.
func walkValue(v protoreflect.Value, fd protoreflect.FieldDescriptor, path string, depth int) error {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if err := checkTimestamp(v.Message(), path); err != nil {
			return err
		}
		return walk(v.Message(), path, depth+1)
	case protoreflect.EnumKind:
		// Neither wire path refuses an undeclared number, so a descriptor
		// lookup is the only thing standing between it and the matcher.
		if fd.Enum().Values().ByNumber(v.Enum()) == nil {
			return &ValidationError{Field: path, Err: fmt.Errorf("%w: %d in %s", ErrInvalidEnum, v.Enum(), fd.Enum().Name())}
		}
	case protoreflect.StringKind:
		return checkString(v.String(), fd, path)
	case protoreflect.BytesKind:
		// By length: String would format the bytes with fmt.Sprint, at up to
		// four characters a byte.
		return bound(len(v.Bytes()), MaxStringBytes, path, "bytes")
	}
	return nil
}

// checkString bounds a string, then holds it to the rule its field takes: the
// free-text rule for the two fields a person reads, the identifier rule for
// every other. Every string rather than a list of them, because a rule that
// names fields refuses envelopes it used to pass the day policy keys on one
// more.
func checkString(s string, fd protoreflect.FieldDescriptor, path string) error {
	if err := bound(len(s), stringLimit(fd), path, "bytes"); err != nil {
		return err
	}
	problem := identifierProblem
	if isFreeText(fd) {
		problem = freeTextProblem
	}
	if err := problem(s); err != nil {
		return &ValidationError{Field: path, Err: err}
	}
	return nil
}

// isFreeText names the two strings ADR-0011 exempts from the identifier rule.
// By the generated types' full names: a message of the same short name that a
// later minor brings under the envelope does not inherit the exemption.
func isFreeText(fd protoreflect.FieldDescriptor) bool {
	switch fd.ContainingMessage().FullName() {
	case fullName((*controlv1.Arguments)(nil)):
		return fd.Name() == "redacted_preview"
	case fullName((*controlv1.Delegation)(nil)):
		return fd.Name() == "reason"
	}
	return false
}

// checkTimestamp refuses a Timestamp the well-known type cannot represent:
// out-of-range nanos, or a second outside year 1 to 9999. protojson refuses
// those, so without this the two entry points admit different envelopes and the
// binary one hands a consumer a time AsTime silently renormalises. A message
// answering to the name without the generated type is one this build cannot
// check, which is not one that passed. The runtime's own message renders the
// offending value and is not stable across builds, so only the reason is kept.
func checkTimestamp(m protoreflect.Message, path string) error {
	if m.Descriptor().FullName() != "google.protobuf.Timestamp" {
		return nil
	}
	if ts, ok := m.Interface().(*timestamppb.Timestamp); ok && ts.CheckValid() == nil {
		return nil
	}
	return &ValidationError{Field: path, Err: fmt.Errorf("%w: not a time this contract can represent", ErrInvalidValue)}
}

// bound refuses a count or a length over its limit. Every collection here is a
// policy input, and MaxEnvelopeBytes leaves room for tens of thousands in one.
func bound(n, limit int, path, unit string) error {
	if n > limit {
		return &ValidationError{Field: path, Err: fmt.Errorf("%w: %d %s", ErrTooLarge, n, unit)}
	}
	return nil
}

// stringLimit gives the redaction preview its own bound, because raising the
// general limit to fit a preview raises it for every identifier too.
func stringLimit(fd protoreflect.FieldDescriptor) int {
	if fd.Name() == "redacted_preview" && fd.ContainingMessage().FullName() == fullName((*controlv1.Arguments)(nil)) {
		return MaxPreviewBytes
	}
	return MaxStringBytes
}

// fullName is a generated message's qualified name, read off its descriptor
// rather than spelled here: it names one message, and it moves with the code
// when the package is renamed.
func fullName(m protoreflect.ProtoMessage) protoreflect.FullName {
	return m.ProtoReflect().Descriptor().FullName()
}

// unknownFieldPath names the first unknown field by number: the number comes
// from the wire and the path from the schema, so no caller text reaches it.
func unknownFieldPath(path string, unknown protoreflect.RawFields) string {
	number, _, n := protowire.ConsumeTag(unknown)
	if n < 0 {
		return join(path, "<unparsable unknown field>")
	}
	return join(path, "<field "+strconv.Itoa(int(number))+">")
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
