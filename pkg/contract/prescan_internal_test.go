// Package contract rather than contract_test, for the reason fuzz_test.go
// gives: the pre-scan's depth bound cannot be reached through v1, which has no
// recursive message, so the test that separates the bound from its absence
// calls scanMessage on a recursive message the runtime ships. FuzzDecode is
// here because one of its properties reads the pre-scan directly.
package contract

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestPrescanRefusesATreeDeeperThanMaxNesting pins the pre-scan's depth bound
// on both sides, as TestWalkRefusesATreeDeeperThanMaxNesting pins the walk's.
func TestPrescanRefusesATreeDeeperThanMaxNesting(t *testing.T) {
	scan := func(levels int) error {
		m := nested(levels)
		b, err := proto.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return scanMessage(b, m.ProtoReflect().Descriptor(), "", 0)
	}
	for _, levels := range []int{0, 1, MaxNesting - 1, MaxNesting} {
		if err := scan(levels); err != nil {
			t.Errorf("a tree %d levels deep was refused with %v; MaxNesting is %d", levels, err, MaxNesting)
		}
	}
	for _, levels := range []int{MaxNesting + 1, 4 * MaxNesting} {
		if err := scan(levels); !errors.Is(err, ErrTooLarge) {
			t.Errorf("a tree %d levels deep was accepted (%v); MaxNesting is %d", levels, err, MaxNesting)
		}
	}
}

// FuzzDecode holds the binary path to what FuzzDecodeJSONThenValidate holds
// the JSON path to: a refusal is a *ValidationError, and what Decode accepts,
// Validate accepts again. It adds two properties. The re-encoding of an
// accepted envelope decodes to an equal message. And the pre-scan never
// refuses as "not wire format" bytes the runtime reads, so everything it
// refuses beyond the parse is refused by a rule it states.
func FuzzDecode(f *testing.F) {
	seedBinaryFromFixtures(f)
	schema := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), "1.0")
	effect := protowire.AppendVarint(protowire.AppendTag(nil, 4, protowire.VarintType), 1<<32+1)
	value := protowire.AppendString(protowire.AppendTag(nil, 2, protowire.BytesType), "v")
	third := protowire.AppendVarint(protowire.AppendTag(nil, 3, protowire.VarintType), 1)
	// A principal whose attributes arrive as the given entries.
	principal := func(entries ...[]byte) []byte {
		var body []byte
		for _, e := range entries {
			body = protowire.AppendBytes(protowire.AppendTag(body, 5, protowire.BytesType), e)
		}
		return bytes.Join([][]byte{schema, protowire.AppendBytes(protowire.AppendTag(nil, 9, protowire.BytesType), body)}, nil)
	}
	for _, seed := range [][]byte{
		nil,
		bytes.Repeat([]byte{0x5a, 0x00}, MaxDelegationDepth+1),
		append(append([]byte{}, schema...), schema...),
		protowire.AppendBytes(protowire.AppendTag(schema, 12, protowire.BytesType), effect),
		principal(value, value),                            // two entries, neither with a key
		principal(bytes.Join([][]byte{value, third}, nil)), // a third field inside an entry
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var ce *codecError
		if err := prescan(data); errors.As(err, &ce) && proto.Unmarshal(data, &controlv1.ActionEnvelope{}) == nil {
			t.Fatalf("the pre-scan refuses %x as not wire format, and the runtime reads it", data)
		}
		env, err := Decode(data)
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("refused %x with %v, which is not a *ValidationError", data, err)
			}
			return
		}
		if again := Validate(env); again != nil {
			t.Fatalf("accepted %x, then refused the same message with %v", data, again)
		}
		wire, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("accepted %x, and it does not encode: %v", data, err)
		}
		if back, err := Decode(wire); err != nil || !proto.Equal(env, back) {
			t.Fatalf("accepted %x; its re-encoding decodes to %v, %v", data, back, err)
		}
	})
}

// seedBinaryFromFixtures adds the reference documents in their binary form.
func seedBinaryFromFixtures(f *testing.F) {
	f.Helper()
	fsys := os.DirFS(fixtureDir)
	names, err := fs.Glob(fsys, "*.json")
	if err != nil || len(names) == 0 {
		f.Fatalf("no fixtures in %s (%v): the corpus would start from nothing", fixtureDir, err)
	}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			f.Fatalf("reading %s: %v", name, err)
		}
		env := &controlv1.ActionEnvelope{}
		if err := protojson.Unmarshal(data, env); err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		wire, err := proto.Marshal(env)
		if err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		f.Add(wire)
	}
}
