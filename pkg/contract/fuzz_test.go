// This file is package contract rather than package contract_test, and the
// reason is the point of the tests in it.
//
// walk's recursion bound cannot be reached through any exported entry point.
// Validate takes an ActionEnvelope, no v1 message is recursive, and the deepest
// chain in the contract is three, so from outside the package the bound is
// indistinguishable from its absence: deleting the depth increment leaves every
// external test green. A bound no test can separate from its absence is not
// enforced, so the tests that separate them are here, where walk is reachable.
//
// The rest of the package's tests stay in package contract_test and use the
// exported surface, which is what keeps that surface honest.
package contract

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"pgregory.net/rapid"
)

const fixtureDir = "../../testdata/contracts/action_envelope"

// FuzzDecodeJSONThenValidate asserts the properties the boundary promises over
// arbitrary bytes: the pair never panics, it never returns an accepted envelope
// and an error at the same time, and what it accepts it accepts again. The last
// one is what would catch a check that depends on map iteration order or on
// anything else that is not the bytes.
//
// A refusal is also required to be classifiable: every error out of DecodeJSON
// is a *ValidationError, so a caller always reaches the field even when no
// sentinel fits.
func FuzzDecodeJSONThenValidate(f *testing.F) {
	seedFromFixtures(f)
	for _, seed := range []string{
		"",
		"{}",
		"null",
		"[]",
		`{"schemaVersion":"1.0"}`,
		`{"schemaVersion":"2.0"}`,
		`{"schemaVersion":"1.0","action":{"effect":99}}`,
		`{"schemaVersion":"1.0","bogus":true}`,
		`{"schemaVersion":"1.0","principal":{"attributes":{"reserved.owner":"x"}}}`,
		`{"schemaVersion":"1.0","delegation":[{"from":"a","to":"b"},{"from":"c","to":"d"}]}`,
		`{"schemaVersion":"1.0","arguments":{"canonicalHash":"sha256:zz"}}`,
		`{"schemaVersion":"1.0","occurredAt":"0001-01-01T00:00:00Z"}`,
		`{"schemaVersion":"1.` + strings.Repeat("9", 40) + `"}`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		env, err := DecodeJSON(data)
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("refused %q with %v, which is not a *ValidationError", data, err)
			}
			return
		}
		if env == nil {
			t.Fatalf("accepted %q and returned no message", data)
		}
		if again := Validate(env); again != nil {
			t.Fatalf("accepted %q, then refused the same message with %v", data, again)
		}
	})
}

// seedFromFixtures adds the reference documents, so the corpus starts from
// input that reaches every check rather than from bytes that die at the parser.
func seedFromFixtures(f *testing.F) {
	f.Helper()

	// Read through an fs.FS rooted at the fixture directory: nothing here
	// builds an operating system path out of a name it read.
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
		f.Add(data)
	}
}

// TestWalkMeasuresBytesByLength: a bytes field is measured by its length, not
// by its printed form. No v1 field is bytes, so this walks the well-known
// wrapper: the walk is generic, and the first bytes field a later minor adds
// meets this branch with nobody writing a test for it then. 0xff is the byte
// fmt.Sprint spells longest, four characters.
func TestWalkMeasuresBytesByLength(t *testing.T) {
	for _, n := range []int{0, 1, MaxStringBytes} {
		if err := walk(wrapperspb.Bytes(bytes.Repeat([]byte{0xff}, n)).ProtoReflect(), "", 0); err != nil {
			t.Errorf("%d bytes of 0xff were refused: %v", n, err)
		}
	}
	err := walk(wrapperspb.Bytes(bytes.Repeat([]byte{0xff}, MaxStringBytes+1)).ProtoReflect(), "", 0)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("%d bytes were accepted (%v); MaxStringBytes is %d", MaxStringBytes+1, err, MaxStringBytes)
	}
}

// TestWalkRefusesATreeDeeperThanMaxNesting pins the bound at the value it
// claims, on both sides of it. MaxNesting counts descents below the root, so a
// tree with exactly that many is walked and one with one more is refused; a
// test that only asserted the refusal would pass against a bound of zero.
func TestWalkRefusesATreeDeeperThanMaxNesting(t *testing.T) {
	for _, levels := range []int{0, 1, 2, MaxNesting - 1, MaxNesting} {
		if err := walk(nested(levels).ProtoReflect(), "", 0); err != nil {
			t.Errorf("a tree %d levels deep was refused with %v; MaxNesting is %d", levels, err, MaxNesting)
		}
	}
	for _, levels := range []int{MaxNesting + 1, MaxNesting + 2, 4 * MaxNesting} {
		err := walk(nested(levels).ProtoReflect(), "", 0)
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("a tree %d levels deep was accepted (%v); MaxNesting is %d", levels, err, MaxNesting)
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field == "" {
			t.Errorf("the refusal at %d levels does not name where the walk stopped: %v", levels, err)
		}
	}
}

// TestWalkDepthBoundHoldsAtEveryDepth is the same rule as a property, so the
// boundary is not the only pair of depths anyone checked. Refusing is required
// to be exactly the depths past the bound: a guard that refused early would
// refuse a message the contract permits, which is a denial of service written
// as a safety check.
func TestWalkDepthBoundHoldsAtEveryDepth(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		levels := rapid.IntRange(0, 3*MaxNesting).Draw(rt, "levels")

		err := walk(nested(levels).ProtoReflect(), "", 0)
		if want := levels > MaxNesting; (err != nil) != want {
			rt.Fatalf("walk of a tree %d levels deep returned %v, want refused = %t (MaxNesting %d)",
				levels, err, want, MaxNesting)
		}
	})
}

// TestWalkStopsAtTheBoundRatherThanAtTheEndOfTheTree is what the bound is for.
// Without it the walk descends as far as the message goes, and it is not only
// the stack that grows: every level allocates a longer path string than the one
// below it, so the memory a caller can make this function spend is quadratic in
// a depth nobody bounded. The allocation ceiling here is what a walk of 33
// levels costs, two orders of magnitude below what walking the whole tree does.
func TestWalkStopsAtTheBoundRatherThanAtTheEndOfTheTree(t *testing.T) {
	const levels, allocCeiling = 5000, 1 << 20

	deep := nested(levels).ProtoReflect()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := walk(deep, "", 0)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a tree %d levels deep was accepted: %v", levels, err)
	}
	if spent := after.TotalAlloc - before.TotalAlloc; spent > allocCeiling {
		t.Errorf("the walk allocated %d bytes on a tree %d levels deep; a walk that stops at MaxNesting (%d) allocates far less than %d",
			spent, levels, MaxNesting, allocCeiling)
	}
}

// nested returns a message whose deepest message sits exactly levels below it.
//
// google.protobuf.Value and google.protobuf.ListValue are the recursive pair
// the runtime already ships, and alternating them adds one level at a time, so
// both sides of an odd bound are reachable. Nothing in v1 is recursive, which
// is the reason this has to be built rather than decoded: the bound guards the
// walk against a message tree the schema does not describe, and a later minor
// adding google.protobuf.Struct to the contract is all it takes to make one
// arrive over the wire.
func nested(levels int) proto.Message {
	value := structpb.NewStringValue("leaf")
	list := &structpb.ListValue{}
	atValue := true
	for range levels {
		if atValue {
			list = &structpb.ListValue{Values: []*structpb.Value{value}}
		} else {
			value = structpb.NewListValue(list)
		}
		atValue = !atValue
	}
	if atValue {
		return value
	}
	return list
}
