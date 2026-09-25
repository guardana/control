package scenario_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/guardana/control/internal/scenario"
)

// The pieces the tests build documents from. Every expected value in a test
// is a literal of its own, never read back from these.
const (
	resultAnswer  = `{"kind":"result","codes":[]}`
	pendingAnswer = `{"kind":"pending","codes":["APPROVAL_PENDING"]}`
	newTrail      = `{"request":"new","kinds":["ACTION_PROPOSED"]}`
	emptyTrail    = `{"request":"new","kinds":[]}`
	noDecision    = `"none"`
)

// doc wraps steps in a document every other member of which is valid.
func doc(steps ...string) string {
	return unescape(`{"kind":"agent-scenario/v1alpha1","about":"a","plane":{"mode":"ENFORCE","bundle":{"id":"demo"}},"steps":[` +
		strings.Join(steps, ",") + `]}`)
}

// unescape turns each BSu into a JSON unicode escape, so a test can spell
// one where its own source text would otherwise hold the character.
func unescape(s string) string { return strings.ReplaceAll(s, "BSu", "\\u") }

// top is a document with the given members after kind.
func top(members string) string {
	return unescape(`{"kind":"agent-scenario/v1alpha1",` + members + `}`)
}

// call is a call step.
func call(args, answer, decided, trail string) string {
	return `{"call":{"tool":"read_file","args":` + args + `,"answer":` + answer +
		`,"decided":` + decided + `,"trail":` + trail + `}}`
}

// plain is a call step that is valid and expects one event.
func plain() string { return call(`{}`, resultAnswer, noDecision, newTrail) }

// pending is a call step answered with a pending approval.
func pending() string { return call(`{}`, pendingAnswer, noDecision, newTrail) }

func accept(t *testing.T, raw string) *scenario.Scenario {
	t.Helper()
	s, err := scenario.Read("a.json", []byte(raw))
	if err != nil {
		t.Fatalf("Read refused a valid document: %v\n%s", err, clip(raw))
	}
	return s
}

// refused holds a refusal to its class and the member path it names. The
// refusal comes back as a value, which a test that needs only the assertions
// may drop.
func refused(t *testing.T, raw string, want scenario.Error, wantPath string) scenario.Refusal {
	t.Helper()
	return refusedNamed(t, "a.json", raw, want, wantPath)
}

func refusedNamed(t *testing.T, name, raw string, want scenario.Error, wantPath string) scenario.Refusal {
	t.Helper()
	s, err := scenario.Read(name, []byte(raw))
	var r *scenario.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("Read = %+v, %v; want a refusal %q at %q\n%s", s, err, want, wantPath, clip(raw))
	}
	if r.Err != want || r.Path != wantPath || !errors.Is(err, want) {
		t.Fatalf("Read refused with %q at %q (%v); want %q at %q\n%s", r.Err, r.Path, err, want, wantPath, clip(raw))
	}
	return *r
}

func isClass(err error, want scenario.Error) bool {
	var r *scenario.Refusal
	return errors.As(err, &r) && r.Err == want
}

// dump renders a scenario with its steps' members dereferenced.
func dump(s *scenario.Scenario) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id=%q about=%q plane=%+v run=%q\n", s.ID, s.About, s.Plane, s.Run)
	for i, st := range s.Steps {
		switch {
		case st.Call != nil:
			c := st.Call
			fmt.Fprintf(&b, "%d call tool=%q args=%q answer=%+v decided={none:%v verdict:%v codes:%q} trail=%+v\n",
				i, c.Tool, c.Args, c.Answer, c.Decided.None, c.Decided.Verdict, c.Decided.Codes, c.Trail)
			for _, o := range c.Decided.Obligations {
				fmt.Fprintf(&b, "  obligation %q %q %v\n", o.Type, o.Params, o.Advisory)
			}
		case st.Approve != nil:
			fmt.Fprintf(&b, "%d approve %+v\n", i, *st.Approve)
		case st.Reject != nil:
			fmt.Fprintf(&b, "%d reject %+v\n", i, *st.Reject)
		case st.Pause != nil:
			fmt.Fprintf(&b, "%d pause %+v\n", i, *st.Pause)
		case st.Unpause != nil:
			fmt.Fprintf(&b, "%d unpause %+v\n", i, *st.Unpause)
		default:
			fmt.Fprintf(&b, "%d empty\n", i)
		}
	}
	return b.String()
}

func clip(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
