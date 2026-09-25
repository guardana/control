package scenario_test

import (
	"strings"
	"testing"

	"github.com/guardana/control/internal/scenario"
)

// TestWrongTypes: every member the format types refuses a value of another
// type as such, naming the member.
func TestWrongTypes(t *testing.T) {
	d := func(decided string) string { return doc(call(`{}`, resultAnswer, decided, newTrail)) }
	runRefusals(t, []refusalCase{
		{"mode", withPlane(`{"mode":5,"bundle":{"id":"demo"}}`), scenario.ErrWrongType, "plane.mode"},
		{"plane", withPlane(`[]`), scenario.ErrWrongType, "plane"},
		{"bundle id", withPlane(`{"mode":"ENFORCE","bundle":{"id":true}}`), scenario.ErrWrongType, "plane.bundle.id"},
		{"digest", withPlane(`{"mode":"ENFORCE","bundle":{"id":"demo","digest":1}}`), scenario.ErrWrongType, "plane.bundle.digest"},
		{"run", top(`"about":"a","plane":{"mode":"ENFORCE","bundle":{"id":"demo"}},"run":true,"steps":[` + plain() + `]`), scenario.ErrWrongType, "run"},
		{"answer kind", doc(call(`{}`, `{"kind":1,"codes":[]}`, noDecision, newTrail)), scenario.ErrWrongType, "steps[0].call.answer.kind"},
		{"verdict", d(`{"verdict":1,"codes":[],"obligations":[]}`), scenario.ErrWrongType, "steps[0].call.decided.verdict"},
		{"decided codes", d(`{"verdict":"DENY","codes":{},"obligations":[]}`), scenario.ErrWrongType, "steps[0].call.decided.codes"},
		{"obligations", d(`{"verdict":"DENY","codes":[],"obligations":{}}`), scenario.ErrWrongType, "steps[0].call.decided.obligations"},
		{"obligation type", d(`{"verdict":"ALLOW","codes":[],"obligations":[{"type":1,"params":{},"advisory":true}]}`), scenario.ErrWrongType, "steps[0].call.decided.obligations[0].type"},
		{"request", doc(call(`{}`, resultAnswer, noDecision, `{"request":0,"kinds":["ACTION_PROPOSED"]}`)), scenario.ErrWrongType, "steps[0].call.trail.request"},
		{"kinds", doc(call(`{}`, resultAnswer, noDecision, `{"request":"new","kinds":"ACTION_PROPOSED"}`)), scenario.ErrWrongType, "steps[0].call.trail.kinds"},
		{"kind item", doc(call(`{}`, resultAnswer, noDecision, `{"request":"new","kinds":[1]}`)), scenario.ErrWrongType, "steps[0].call.trail.kinds[0]"},
		{"approver", doc(pending(), `{"approve":{"step":0,"approver":7}}`), scenario.ErrWrongType, "steps[1].approve.approver"},
		{"approve reason", doc(pending(), `{"approve":{"step":0,"approver":"a","reason":7}}`), scenario.ErrWrongType, "steps[1].approve.reason"},
		{"approve", doc(pending(), `{"approve":[]}`), scenario.ErrWrongType, "steps[1].approve"},
		{"provider", doc(plain(), `{"pause":{"provider":7}}`), scenario.ErrWrongType, "steps[1].pause.provider"},
		{"pause reason", doc(plain(), `{"pause":{"global":true,"reason":7}}`), scenario.ErrWrongType, "steps[1].pause.reason"},
		{"unpause step", doc(plain(), `{"pause":{"global":true}}`, `{"unpause":{"step":"1"}}`), scenario.ErrWrongType, "steps[2].unpause.step"},
		{"unpause", doc(plain(), `{"unpause":1}`), scenario.ErrWrongType, "steps[1].unpause"},
	})
}

// TestRefusalText: a refusal renders its class, its member, its detail and
// its bounded quote on one line.
func TestRefusalText(t *testing.T) {
	r := refused(t, doc(call(`{}`, `{"kind":"blocked","codes":["`+strings.Repeat("Z", 70)+`"]}`, noDecision, newTrail)),
		scenario.ErrValue, "steps[0].call.answer.codes[0]")
	want := `scenario: a value outside its set at steps[0].call.answer.codes[0]: not a registered reason code "` + strings.Repeat("Z", 64) + `"...`
	if got := r.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	r = refusedNamed(t, "A.json", doc(plain()), scenario.ErrName, "")
	if got, want := r.Error(), `scenario: the file name is not a scenario id: want [a-z0-9][a-z0-9-]{0,63}.json "A.json"`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	r = refused(t, strings.Replace(doc(plain()), `"about":"a"`, `"about":1`, 1), scenario.ErrWrongType, "about")
	if got, want := r.Error(), `scenario: a value of the wrong type at about: want a string`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	r = refused(t, doc(call(`{"`+strings.Repeat("k", 70)+`":null}`, resultAnswer, noDecision, newTrail)), scenario.ErrNull,
		`steps[0].call.args["`+strings.Repeat("k", 64)+`..."]`)
	if got, want := r.Error(), `scenario: null is refused at steps[0].call.args["`+strings.Repeat("k", 64)+`..."]`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
