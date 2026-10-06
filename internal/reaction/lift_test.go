package reaction_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/reaction"
)

const runID = "run-0123456789abcdef0123456789abcdef"

func validLift() reaction.Lift {
	return reaction.Lift{Version: "1.0", ListID: "list-1", RouteDigest: procDigest, RunID: runID, ThroughLine: 12}
}

// liftCanonical is validLift's payload, written out by hand.
const liftCanonical = `{"kind":"reaction-lift/v1alpha1","list_id":"list-1","route_digest":"` + procDigest +
	`","run_id":"` + runID + `","through_line":12,"version":"1.0"}`

func TestLiftPayloadIsCanonical(t *testing.T) {
	body, err := validLift().Payload()
	if err != nil || string(body) != liftCanonical {
		t.Fatalf("Payload = %s, %v\nwant      %s", body, err, liftCanonical)
	}
}

func TestSignLiftVerifies(t *testing.T) {
	env, err := reaction.SignLift(validLift(), liftKey())
	if err != nil {
		t.Fatalf("SignLift: %v", err)
	}
	if env.PayloadType != "application/vnd.agent-reaction-lift+json" || string(env.Payload) != liftCanonical ||
		len(env.Signatures) != 1 || env.Signatures[0].KeyID != keyIDOf(liftKey()) {
		t.Fatalf("envelope = %q %s %+v", env.PayloadType, env.Payload, env.Signatures)
	}
	got, err := reaction.VerifyLift(env, pubOf(liftKey()))
	if err != nil || got != validLift() {
		t.Fatalf("VerifyLift = %+v, %v", got, err)
	}
	_, err = reaction.VerifyLift(env, pubOf(routeKey()))
	expectOnly(t, "a lift under the route key", err, reaction.ErrLiftKey, envelopeRefusals())
	env.PayloadType = reaction.RoutePayloadType
	_, err = reaction.VerifyLift(env, pubOf(liftKey()))
	expectOnly(t, "a lift labelled a route", err, reaction.ErrLiftPayloadType, envelopeRefusals())
}

// liftSigned signs body as a lift under the lift key, whatever it holds.
func liftSigned(t *testing.T, body string) reaction.Envelope {
	t.Helper()
	sig, err := bundle.SignBytesAs(reaction.LiftPayloadType, []byte(body), liftKey())
	if err != nil {
		t.Fatal(err)
	}
	return reaction.Envelope{PayloadType: reaction.LiftPayloadType, Payload: []byte(body), Signatures: []reaction.Signature{{KeyID: keyIDOf(liftKey()), Signature: sig}}}
}

func TestVerifyLiftRefuses(t *testing.T) {
	edit := func(old, repl string) string {
		t.Helper()
		if !strings.Contains(liftCanonical, old) {
			t.Fatalf("the lift holds no %q", old)
		}
		return strings.Replace(liftCanonical, old, repl, 1)
	}
	for _, c := range []struct {
		name, body string
		want       error
	}{
		{"an array", `[]`, reaction.ErrLiftJSON},
		{"a member twice", edit(`"through_line":12`, `"through_line":12,"through_line":12`), reaction.ErrLiftRepeated},
		{"another kind", edit(`reaction-lift/v1alpha1`, `reaction-route/v1alpha1`), reaction.ErrLiftKind},
		{"an unknown member", edit(`"through_line":12`, `"through_line":12,"x":1`), reaction.ErrLiftMember},
		{"no run", edit(`"run_id":"`+runID+`",`, ``), reaction.ErrLiftMember},
		{"version 2.0", edit(`"1.0"`, `"2.0"`), reaction.ErrLiftVersion},
		{"version 1", edit(`"1.0"`, `"1"`), reaction.ErrLiftVersion},
		{"a version that is a number", edit(`"1.0"`, `1`), reaction.ErrLiftVersion},
		{"an empty list id", edit(`"list-1"`, `""`), reaction.ErrLiftValue},
		{"a list id with a line break", edit(`"list-1"`, `"list\n1"`), reaction.ErrLiftValue},
		{"a route digest in capitals", edit(procDigest, strings.ToUpper(procDigest)), reaction.ErrLiftValue},
		{"an empty run", edit(`"`+runID+`"`, `""`), reaction.ErrLiftValue},
		{"through line 0", edit(`"through_line":12`, `"through_line":0`), reaction.ErrLiftLine},
		{"a negative through line", edit(`"through_line":12`, `"through_line":-12`), reaction.ErrLiftLine},
		{"through line 2^53", edit(`"through_line":12`, `"through_line":9007199254740992`), reaction.ErrLiftLine},
		{"a through line with a fraction", edit(`"through_line":12`, `"through_line":12.0`), reaction.ErrLiftLine},
		{"a through line as a string", edit(`"through_line":12`, `"through_line":"12"`), reaction.ErrLiftLine},
		{"members out of order", `{"list_id":"list-1","kind":"reaction-lift/v1alpha1","route_digest":"` + procDigest +
			`","run_id":"` + runID + `","through_line":12,"version":"1.0"}`, reaction.ErrLiftNotCanonical},
		{"white space", edit(`,"version"`, `, "version"`), reaction.ErrLiftNotCanonical},
		{"an unpaired surrogate", edit(`"list-1"`, `"list\ud8001"`), reaction.ErrLiftJSON},
	} {
		_, err := reaction.VerifyLift(liftSigned(t, c.body), pubOf(liftKey()))
		expectOnly(t, c.name, err, c.want, liftRefusals())
	}
}

func TestLiftBounds(t *testing.T) {
	for _, c := range []struct {
		name string
		with func(n int) reaction.Lift
		at   int
	}{
		{"list_id", func(n int) reaction.Lift { l := validLift(); l.ListID = strings.Repeat("l", n); return l }, 128},
		{"run_id", func(n int) reaction.Lift { l := validLift(); l.RunID = strings.Repeat("r", n); return l }, 64},
	} {
		if _, err := c.with(c.at).Payload(); err != nil {
			t.Errorf("%s of %d bytes: %v", c.name, c.at, err)
		}
		body := strings.Replace(liftCanonical, map[string]string{"list_id": `"list-1"`, "run_id": `"` + runID + `"`}[c.name],
			`"`+strings.Repeat("x", c.at+1)+`"`, 1)
		_, err := reaction.VerifyLift(liftSigned(t, body), pubOf(liftKey()))
		expectOnly(t, c.name+" one byte over", err, reaction.ErrLiftValue, liftRefusals())
	}
	for _, line := range []int64{1, 9007199254740991} {
		l := validLift()
		l.ThroughLine = line
		env, err := reaction.SignLift(l, liftKey())
		if err != nil {
			t.Fatalf("through line %d: %v", line, err)
		}
		if got, err := reaction.VerifyLift(env, pubOf(liftKey())); err != nil || got.ThroughLine != line {
			t.Errorf("through line %d read back as %d, %v", line, got.ThroughLine, err)
		}
	}
}

func TestLiftBodyBound(t *testing.T) {
	over := reaction.Envelope{PayloadType: reaction.LiftPayloadType, Payload: []byte(strings.Repeat(" ", 4097)),
		Signatures: []reaction.Signature{{KeyID: keyIDOf(liftKey()), Signature: make([]byte, ed25519.SignatureSize)}}}
	_, err := reaction.VerifyLift(over, pubOf(liftKey()))
	expectOnly(t, "a body of 4097 bytes", err, reaction.ErrLiftTooLarge, envelopeRefusals())
	over.Payload = over.Payload[:4096]
	_, err = reaction.VerifyLift(over, pubOf(liftKey()))
	expectOnly(t, "a body of 4096 bytes", err, reaction.ErrLiftSignature, envelopeRefusals())
}

func TestPayloadRefusesALiftNoReaderTakes(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*reaction.Lift)
	}{
		{"the zero lift", func(l *reaction.Lift) { *l = reaction.Lift{} }},
		{"no version", func(l *reaction.Lift) { l.Version = "" }},
		{"through line 0", func(l *reaction.Lift) { l.ThroughLine = 0 }},
		{"through line 2^53", func(l *reaction.Lift) { l.ThroughLine = 1 << 53 }},
		{"invalid UTF-8 in the list id", func(l *reaction.Lift) { l.ListID = "list\xff" }},
		{"a short route digest", func(l *reaction.Lift) { l.RouteDigest = procDigest[:20] }},
	} {
		l := validLift()
		c.edit(&l)
		if body, err := l.Payload(); err == nil {
			t.Errorf("%s: payload %s", c.name, body)
		}
		if _, err := reaction.SignLift(l, liftKey()); err == nil {
			t.Errorf("%s: signed", c.name)
		}
	}
}
