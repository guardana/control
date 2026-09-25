package contract_test

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// Map entries are written by hand here: no encoder puts a third field into an
// entry, a key on the wrong wire type or one key into two entries, so
// proto.Marshal cannot produce these inputs.
func mapEntry(fields ...[]byte) []byte { return lenField(fieldAttributes, concat(fields...)) }
func entryKey(s string) []byte         { return lenField(1, []byte(s)) }
func entryValue(s string) []byte       { return lenField(2, []byte(s)) }

// attributeEntries returns a builder of the base envelope whose principal's
// attributes arrive as the entries it is given.
func attributeEntries(t *testing.T) func(entries ...[]byte) []byte {
	t.Helper()
	noPrincipal := valid()
	noPrincipal.Principal = nil
	head, principal := marshalled(t, noPrincipal), marshalled(t, valid().Principal)
	return func(entries ...[]byte) []byte {
		return concat(head, lenField(fieldPrincipal, concat(principal, concat(entries...))))
	}
}

// TestDecodeRefusesWhatAMapEntryWouldDrop is ADR-0011's map-entry rule. Inside
// a map entry the runtime keeps the key and the value, each read at its own
// wire type, and drops anything else without a trace, so no walk after the
// parse can refuse it. That was the one exception ADR-0002 recorded to
// "unknown fields are refused". The pre-scan reads the entry first and refuses
// what the runtime would drop as an unknown field of the map. It names the
// map, because an entry's position among the sorted keys is not known before
// the parse.
func TestDecodeRefusesWhatAMapEntryWouldDrop(t *testing.T) {
	attributes := attributeEntries(t)
	group := concat(protowire.AppendTag(nil, 3, protowire.StartGroupType), protowire.AppendTag(nil, 3, protowire.EndGroupType))
	fixedKey := protowire.AppendFixed32(protowire.AppendTag(nil, 1, protowire.Fixed32Type), 7)
	for _, tc := range []struct {
		name  string
		wire  []byte
		reads map[string]string // what the runtime makes of the attributes
	}{
		{"a third field", attributes(mapEntry(entryKey("team"), entryValue("ops"), varintField(3, 1))),
			map[string]string{"team": "ops"}},
		{"a third field sent as a group", attributes(mapEntry(entryKey("team"), entryValue("ops"), group)),
			map[string]string{"team": "ops"}},
		{"the key sent as a varint", attributes(mapEntry(varintField(1, 7), entryValue("ops"))),
			map[string]string{"": "ops"}},
		{"the key sent as a fixed32", attributes(mapEntry(fixedKey, entryValue("ops"))),
			map[string]string{"": "ops"}},
		{"the value sent as a varint", attributes(mapEntry(entryKey("team"), varintField(2, 7))),
			map[string]string{"team": ""}},
		// The second key reads as the empty key, which the first entry holds
		// already, so the two entries collapse into one.
		{"an entry with no key, then one with a varint key",
			attributes(mapEntry(entryValue("one")), mapEntry(varintField(1, 7), entryValue("two"))),
			map[string]string{"": "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &controlv1.ActionEnvelope{}
			err := proto.Unmarshal(tc.wire, parsed)
			if got := parsed.GetPrincipal().GetAttributes(); err != nil || contract.Validate(parsed) != nil || !maps.Equal(got, tc.reads) {
				t.Fatalf("the runtime does not drop the field into an envelope Validate accepts (%v, %v, %q), so this proves nothing",
					err, contract.Validate(parsed), got)
			}
			env, err := contract.Decode(tc.wire)
			assertRefused(t, err, contract.ErrUnknownField, "principal.attributes")
			if env != nil {
				t.Error("a message came back with a refusal made before the parse")
			}
		})
	}

	// The budgets map holds numbers, and a value sent length-delimited is
	// dropped there as well.
	budget := concat(marshalled(t, valid()),
		lenField(fieldContext, lenField(fieldBudgets, concat(entryKey("calls"), lenField(2, []byte{0x03})))))
	parsed := &controlv1.ActionEnvelope{}
	err := proto.Unmarshal(budget, parsed)
	if got := parsed.GetContext().GetBudgets(); err != nil || contract.Validate(parsed) != nil || !maps.Equal(got, map[string]int64{"calls": 0}) {
		t.Fatalf("the runtime does not drop the budget's value (%v, %v, %v), so this proves nothing", err, contract.Validate(parsed), got)
	}
	env, err := contract.Decode(budget)
	assertRefused(t, err, contract.ErrUnknownField, "context.budgets")
	if env != nil {
		t.Error("a message came back with a refusal made before the parse")
	}
}

// TestDecodeRefusesTwoEntriesTheRuntimeReadsAsOneKey is ADR-0011's one-key
// rule. The runtime keeps the last of two entries filed under one key, and
// files an entry with no key under the empty key. Two byte strings then become
// one accepted envelope, and a reader that kept the first entry would match
// another value. The JSON path refuses a duplicate key in its codec, so this
// refusal carries no sentinel either, and it names the map.
func TestDecodeRefusesTwoEntriesTheRuntimeReadsAsOneKey(t *testing.T) {
	attributes := attributeEntries(t)
	for _, tc := range []struct {
		name  string
		wire  []byte
		reads map[string]string
	}{
		{"one key in two entries", attributes(mapEntry(entryKey("a"), entryValue("1")), mapEntry(entryKey("a"), entryValue("2"))),
			map[string]string{"a": "2"}},
		// Not neighbours: a check against the previous entry alone passes it.
		{"one key in two entries with another between",
			attributes(mapEntry(entryKey("a"), entryValue("1")), mapEntry(entryKey("b"), entryValue("2")), mapEntry(entryKey("a"), entryValue("3"))),
			map[string]string{"a": "3", "b": "2"}},
		// Neither entry has a key field at all.
		{"two entries with no key", attributes(mapEntry(entryValue("one")), mapEntry(entryValue("two"))),
			map[string]string{"": "two"}},
		{"an entry with no key, then one with the empty key",
			attributes(mapEntry(entryValue("one")), mapEntry(entryKey(""), entryValue("two"))),
			map[string]string{"": "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &controlv1.ActionEnvelope{}
			err := proto.Unmarshal(tc.wire, parsed)
			if got := parsed.GetPrincipal().GetAttributes(); err != nil || contract.Validate(parsed) != nil || !maps.Equal(got, tc.reads) {
				t.Fatalf("the runtime does not collapse the entries into an envelope Validate accepts (%v, %v, %q), so this proves nothing",
					err, contract.Validate(parsed), got)
			}
			env, err := contract.Decode(tc.wire)
			assertRefusedBeforeTheParse(t, env, err, "principal.attributes")
		})
	}

	// The accepting side, so the rows above are refused for the key and not
	// for arriving by hand.
	for _, tc := range []struct {
		name string
		wire []byte
	}{
		{"two keys", attributes(mapEntry(entryKey("a"), entryValue("1")), mapEntry(entryKey("b"), entryValue("2")))},
		{"a key and a longer one", attributes(mapEntry(entryKey("a"), entryValue("1")), mapEntry(entryKey("ab"), entryValue("2")))},
		{"one entry with no key", attributes(mapEntry(entryValue("one")))},
		{"the value before the key", attributes(mapEntry(entryValue("ops"), entryKey("team")))},
	} {
		if _, err := contract.Decode(tc.wire); err != nil {
			t.Errorf("%s: refused with %v", tc.name, err)
		}
	}
}

// TestDecodeRefusesExactlyTheEntriesTheRuntimeCollapses states ADR-0011's
// one-key rule against the runtime rather than against a copy of the rule.
// Over entries nobody chose, Decode refuses two entries as one key exactly
// when the runtime's map holds fewer entries than were sent. "A" and "a" are
// two keys to the runtime; the walk's fold rule refuses that pair after the
// parse, with the message.
func TestDecodeRefusesExactlyTheEntriesTheRuntimeCollapses(t *testing.T) {
	attributes := attributeEntries(t)
	const noKey = "<no key field>"
	keys := []string{noKey, "", "a", "A", "b"}
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 6).Draw(rt, "entries")
		entries := make([][]byte, 0, n)
		for i := range n {
			fields := [][]byte{entryValue(strconv.Itoa(i))}
			if key := rapid.SampledFrom(keys).Draw(rt, "key"); key != noKey {
				fields = append(fields, entryKey(key))
				if rapid.Bool().Draw(rt, "key first") {
					slices.Reverse(fields)
				}
			}
			entries = append(entries, mapEntry(fields...))
		}
		wire := attributes(entries...)

		parsed := &controlv1.ActionEnvelope{}
		if err := proto.Unmarshal(wire, parsed); err != nil {
			rt.Fatalf("the runtime refuses %x: %v", wire, err)
		}
		kept := len(parsed.GetPrincipal().GetAttributes())
		env, err := contract.Decode(wire)
		var ve *contract.ValidationError
		asOneKey := env == nil && errors.As(err, &ve) && ve.Field == "principal.attributes"
		if asOneKey != (kept < n) {
			rt.Fatalf("%d entries sent, %d kept by the runtime; Decode = %v, message returned %t", n, kept, err, env != nil)
		}
	})
}

// TestEveryMapTheEnvelopeCarriesIsKeyedByString pins what the pre-scan reads as
// an entry's key: the bytes of a string. The runtime reads a number key at the
// number's width, so 2^32+1 and 1 are one int32 key. A map keyed by a number
// fails here until the pre-scan reads keys that way.
func TestEveryMapTheEnvelopeCarriesIsKeyedByString(t *testing.T) {
	var found []string
	visited := map[protoreflect.FullName]bool{}
	var visit func(md protoreflect.MessageDescriptor, prefix string)
	visit = func(md protoreflect.MessageDescriptor, prefix string) {
		if visited[md.FullName()] {
			return
		}
		visited[md.FullName()] = true
		fields := md.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)
			path := prefix + string(fd.Name())
			switch {
			case fd.IsMap():
				found = append(found, path)
				if kind := fd.MapKey().Kind(); kind != protoreflect.StringKind {
					t.Errorf("%s is keyed by %s, and the pre-scan reads a key as the bytes of a string", path, kind)
				}
				if value := fd.MapValue().Message(); value != nil {
					visit(value, path+"[].")
				}
			case fd.Message() != nil:
				visit(fd.Message(), path+".")
			}
		}
	}
	visit((&controlv1.ActionEnvelope{}).ProtoReflect().Descriptor(), "")
	if len(found) < 3 {
		t.Fatalf("found the maps %v, and the envelope carries three; the descriptor walk is broken", found)
	}
}
