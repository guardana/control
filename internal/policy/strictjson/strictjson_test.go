package strictjson_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/guardana/control/internal/policy/strictjson"
)

func refusals() []error {
	return []error{strictjson.ErrNotObject, strictjson.ErrRepeated, strictjson.ErrUnknown, strictjson.ErrMissing}
}

func expectOnly(t *testing.T, what string, err, want error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: accepted, want %q", what, want)
		return
	}
	for _, s := range refusals() {
		if got, wanted := errors.Is(err, s), errors.Is(s, want); got != wanted {
			t.Errorf("%s: errors.Is(%q, %q) = %v, want %v", what, err, s, got, wanted)
		}
	}
}

func TestReadObjectKeepsEachValueAsWritten(t *testing.T) {
	o, err := strictjson.ReadObject([]byte(" {\"a\" : \"x\\u0079\", \"b\":[1, 2],\n\"c\":null} \n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": `"x\u0079"`, "b": `[1, 2]`, "c": `null`}
	if len(o) != len(want) {
		t.Fatalf("%d members, want %d", len(o), len(want))
	}
	for name, raw := range want {
		if string(o[name]) != raw {
			t.Errorf("member %s = %s, want %s", name, o[name], raw)
		}
	}
	if err := o.Only("a", "b", "c"); err != nil {
		t.Errorf("Only: %v", err)
	}
	if err := o.Require("a", "b", "c"); err != nil {
		t.Errorf("Require: %v", err)
	}
}

func TestReadObjectRefusesWhatADecoderHides(t *testing.T) {
	for _, c := range []struct {
		name, raw string
		want      error
	}{
		{"a member twice", `{"a":1,"a":1}`, strictjson.ErrRepeated},
		{"a member twice, once escaped", `{"a":1,"\u0061":2}`, strictjson.ErrRepeated},
		{"a member twice, the later one null", `{"a":1,"b":2,"a":null}`, strictjson.ErrRepeated},
		{"an array", `[]`, strictjson.ErrNotObject},
		{"a string", `"a"`, strictjson.ErrNotObject},
		{"nothing", ``, strictjson.ErrNotObject},
		{"white space alone", " \n", strictjson.ErrNotObject},
		{"a second object", `{"a":1}{}`, strictjson.ErrNotObject},
		{"a word after it", `{"a":1} x`, strictjson.ErrNotObject},
		{"an unclosed object", `{"a":1`, strictjson.ErrNotObject},
		{"a member with no value", `{"a":}`, strictjson.ErrNotObject},
		{"a trailing comma", `{"a":1,}`, strictjson.ErrNotObject},
		{"a key that is not a string", `{1:1}`, strictjson.ErrNotObject},
	} {
		o, err := strictjson.ReadObject([]byte(c.raw))
		expectOnly(t, c.name, err, c.want)
		if o != nil {
			t.Errorf("%s: an object beside the refusal", c.name)
		}
	}
}

func TestOnlyAndRequire(t *testing.T) {
	o, err := strictjson.ReadObject([]byte(`{"a":1,"B":2}`))
	if err != nil {
		t.Fatal(err)
	}
	expectOnly(t, "a member in another case", o.Only("a", "b"), strictjson.ErrUnknown)
	expectOnly(t, "a member of another case missing", o.Require("a", "b"), strictjson.ErrMissing)
	if err := o.Only("a", "B", "c"); err != nil {
		t.Errorf("Only with a name not present: %v", err)
	}
	if err := o.Require("a"); err != nil {
		t.Errorf("Require of a present member: %v", err)
	}
	empty, err := strictjson.ReadObject([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := empty.Only("a"); err != nil {
		t.Errorf("an empty object: %v", err)
	}
	expectOnly(t, "an empty object", empty.Require("a"), strictjson.ErrMissing)
}

func TestString(t *testing.T) {
	for raw, want := range map[string]string{`"x"`: "x", `""`: "", `"\u0079"`: "y"} {
		if s, ok := strictjson.String(json.RawMessage(raw)); !ok || s != want {
			t.Errorf("String(%s) = %q, %v; want %q", raw, s, ok, want)
		}
	}
	for _, raw := range []string{`null`, `1`, `true`, `{}`, `["x"]`, ``, `"x`} {
		if s, ok := strictjson.String(json.RawMessage(raw)); ok || s != "" {
			t.Errorf("String(%s) = %q, %v; want a refusal", raw, s, ok)
		}
	}
}

func TestIsVersion(t *testing.T) {
	for _, v := range []string{"1.0", "1.7", "1.4294967295"} {
		if !strictjson.IsVersion(v, "1") {
			t.Errorf("IsVersion(%q, 1) = false, want true", v)
		}
	}
	for _, v := range []string{
		"", "1", "1.", ".0", "1.x", "1.0.0", "1.-3", "1.+3", "1.0_0", "1. 0", " 1.0", "1.0 ",
		"01.0", "2.0", "10.0", "1.4294967296", "١.0",
	} {
		if strictjson.IsVersion(v, "1") {
			t.Errorf("IsVersion(%q, 1) = true, want false", v)
		}
	}
	if strictjson.IsVersion("+1.0", "+1") || strictjson.IsVersion("x.0", "x") {
		t.Error("a major that is no decimal is accepted when the caller names it")
	}
}
