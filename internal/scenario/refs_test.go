package scenario_test

import (
	"errors"
	"testing"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/scenario"
)

func withArgs(args string) *scenario.Call {
	return &scenario.Call{Args: []byte(args)}
}

// TestSubstitutionReplacesInsideStrings: each reference is replaced by its
// step's output, the string holding it is encoded again, and every other
// token keeps the bytes it was written with.
func TestSubstitutionReplacesInsideStrings(t *testing.T) {
	s := accept(t, doc(plain(), plain(), call(
		`{ "a" : "x${step[0].output}y${step[1].output}${step[0].output}", "n":12345678901234567890, "f":1e400, "k":"plainBSu0041" }`,
		resultAnswer, noDecision, newTrail)))
	got, err := s.Steps[2].Call.Arguments(map[int]string{0: "Z", 1: "tab\there"})
	want := `{ "a" : "xZytab\thereZ", "n":12345678901234567890, "f":1e400, "k":"plainBSu0041" }`
	if err != nil || string(got) != unescape(want) {
		t.Errorf("Arguments = %q, %v; want %q", got, err, unescape(want))
	}
}

// TestSubstitutionReencodesTheWholeString: escapes the author wrote in a
// string that holds a reference are decoded with it and encoded as canon
// encodes, so the other escapes in that one string change spelling.
func TestSubstitutionReencodesTheWholeString(t *testing.T) {
	s := accept(t, doc(plain(), call(`{"a":"BSu0041\/\n${step[0].output}","b":"BSu0041\/"}`, resultAnswer, noDecision, newTrail)))
	got, err := s.Steps[1].Call.Arguments(map[int]string{0: "é "})
	want := "{\"a\":\"A/\\né \",\"b\":\"BSu0041\\/\"}"
	if err != nil || string(got) != unescape(want) {
		t.Errorf("Arguments = %q, %v; want %q", got, err, unescape(want))
	}
}

// TestOutputGoesInLiterally: an output that holds a reference and the bytes
// a markup encoder would escape is put in as text, never read again, and the
// arguments hash to what canon makes of the bytes written out by hand.
func TestOutputGoesInLiterally(t *testing.T) {
	s := accept(t, doc(plain(), plain(), call(`{"q":"${step[1].output}|${step[0].output}"}`, resultAnswer, noDecision, newTrail)))
	got, err := s.Steps[2].Call.Arguments(map[int]string{0: "A", 1: `${step[0].output}"</x>& `})
	if err != nil {
		t.Fatalf("Arguments: %v", err)
	}
	want := `{"q":"${step[0].output}\"</x>& |A"}`
	if string(got) != want {
		t.Fatalf("Arguments = %q, want %q", got, want)
	}
	gotHash, err := canon.ArgumentsHashV1(got)
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := canon.ArgumentsHashV1([]byte(`{"q":"${step[0].output}\"</x>& |A"}`))
	if err != nil || gotHash != wantHash {
		t.Errorf("hash = %s, want %s (%v)", gotHash, wantHash, err)
	}
	s = accept(t, doc(plain(), plain(), call(`{"q":"${step[0].output}|${step[1].output}"}`, resultAnswer, noDecision, newTrail)))
	got, err = s.Steps[2].Call.Arguments(map[int]string{0: "${step[1].output}", 1: "B"})
	if want := `{"q":"${step[1].output}|B"}`; err != nil || string(got) != want {
		t.Errorf("Arguments = %q, %v; want %q", got, err, want)
	}
}

// TestSubstitutionNeedsEveryOutput: an output the caller does not hand in,
// or one canon cannot encode, is an error, never an empty string.
func TestSubstitutionNeedsEveryOutput(t *testing.T) {
	s := accept(t, doc(plain(), plain(), call(`{"q":"${step[0].output}${step[1].output}"}`, resultAnswer, noDecision, newTrail)))
	c := s.Steps[2].Call
	for _, outputs := range []map[int]string{nil, {0: "a"}, {1: "b"}, {0: "a", 1: "\xff"}} {
		got, err := c.Arguments(outputs)
		if !errors.Is(err, scenario.ErrOutput) || got != nil {
			t.Errorf("Arguments(%v) = %q, %v; want ErrOutput", outputs, got, err)
		}
	}
	if got, err := c.Arguments(map[int]string{0: "", 1: ""}); err != nil || string(got) != `{"q":""}` {
		t.Errorf("Arguments with empty outputs = %q, %v", got, err)
	}
}

// TestArgumentsWithoutReferences are returned as written, and a call built
// by hand carries none.
func TestArgumentsWithoutReferences(t *testing.T) {
	got, err := withArgs(`{"a" : "${x}"}`).Arguments(nil)
	if err != nil || string(got) != `{"a" : "${x}"}` {
		t.Errorf("Arguments = %q, %v", got, err)
	}
}

// TestReferenceRefusals: a reference names an earlier call answered with a
// result, is spelled exactly, and stands only in a string value.
func TestReferenceRefusals(t *testing.T) {
	ref := func(args string) string { return doc(plain(), call(args, resultAnswer, noDecision, newTrail)) }
	runRefusals(t, []refusalCase{
		{"same step", ref(`{"q":"${step[1].output}"}`), scenario.ErrStep, "steps[1].call.args.q"},
		{"later step", ref(`{"q":"${step[2].output}"}`), scenario.ErrStep, "steps[1].call.args.q"},
		{"huge step", ref(`{"q":"${step[123456789012345678901234567890].output}"}`), scenario.ErrStep, "steps[1].call.args.q"},
		{"first step itself", doc(call(`{"q":"${step[0].output}"}`, resultAnswer, noDecision, newTrail)), scenario.ErrStep, "steps[0].call.args.q"},
		{"a pause", doc(plain(), `{"pause":{"global":true}}`, call(`{"q":"${step[1].output}"}`, resultAnswer, noDecision, newTrail)), scenario.ErrStep, "steps[2].call.args.q"},
		{"a blocked call", doc(call(`{}`, `{"kind":"blocked","codes":["RULE_DENY"]}`, noDecision, newTrail), call(`{"q":"${step[0].output}"}`, resultAnswer, noDecision, newTrail)), scenario.ErrStep, "steps[1].call.args.q"},
		{"a pending call", doc(pending(), call(`{"q":"${step[0].output}"}`, resultAnswer, noDecision, newTrail)), scenario.ErrStep, "steps[1].call.args.q"},
		{"member name", ref(`{"${step[0].output}":"q"}`), scenario.ErrReference, `steps[1].call.args["${step[0].output}"]`},
		{"member name literal", ref(`{"a${":"q"}`), scenario.ErrReference, `steps[1].call.args["a${"]`},
		{"unterminated", ref(`{"q":"${step[0].output"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"bare", ref(`{"q":"${"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"other name", ref(`{"q":"${step[0].result}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"other root", ref(`{"q":"${env[0].output}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"space", ref(`{"q":"${ step[0].output}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"space inside", ref(`{"q":"${step[ 0].output}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"leading zero", ref(`{"q":"${step[00].output}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"negative", ref(`{"q":"${step[-1].output}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"no digits", ref(`{"q":"${step[].output}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"second one bad", ref(`{"q":"${step[0].output}${step[0]}"}`), scenario.ErrReference, "steps[1].call.args.q"},
		{"nested", ref(`{"q":[{"r":"x${y}"}]}`), scenario.ErrReference, "steps[1].call.args.q[0].r"},
		{"escaped", ref(`{"q":"BSu0024{step[9].output}"}`), scenario.ErrStep, "steps[1].call.args.q"},
	})
	for _, args := range []string{
		`{"q":"$step[0].output"}`, `{"q":"$ {"}`, `{"q":"{step[0].output}"}`, `{"q":"$"}`, `{"q":"}${step[0].output}$"}`,
	} {
		accept(t, ref(unescape(args)))
	}
}

// TestReferencesOnlyInArguments: parameters and the tool name are compared,
// not sent, and a "${" there is kept as text.
func TestReferencesOnlyInArguments(t *testing.T) {
	s := accept(t, doc(`{"call":{"tool":"${t}","args":{},"answer":`+resultAnswer+
		`,"decided":{"verdict":"ALLOW","codes":[],"obligations":[{"type":"emit_alert","params":{"${x}":"${step[9].output}"},"advisory":true}]},"trail":`+newTrail+`}}`))
	if got := s.Steps[0].Call.Tool; got != "${t}" {
		t.Errorf("Tool = %q", got)
	}
}
