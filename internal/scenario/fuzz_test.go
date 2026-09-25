package scenario_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/guardana/control/internal/scenario"
)

// FuzzRead: no input panics the reader; every refusal is a *Refusal whose
// quote stays within MaxQuoteBytes; an accepted document reads again as the
// same scenario, holds its arguments and parameters as bytes of the input,
// and substitutes every reference once each earlier call's output is given.
func FuzzRead(f *testing.F) {
	for _, seed := range []string{
		everyStep,
		doc(plain()),
		doc(plain(), call(`{"q":"${step[0].output}|x","n":1e400}`, resultAnswer, noDecision, newTrail)),
		doc(pending(), `{"approve":{"step":0,"approver":"a"}}`, call(`{}`, resultAnswer, noDecision, `{"request":"step[0]","kinds":[]}`)),
		doc(plain(), `{"pause":{"provider":"p","action":"tool","name":"n"}}`, `{"unpause":{"step":1}}`),
		`{"zzz":1,"kind":"agent-scenario/v2"}`,
		`{"kind":"agent-scenario/v1alpha1","about":null}`,
		doc(call(`{"p":"\ud800"}`, resultAnswer, noDecision, newTrail)),
		doc(call("{\"p\":\"\xff\"}", resultAnswer, noDecision, newTrail)),
		doc(plain()) + " x",
		`[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := scenario.Read("f.json", raw)
		if err != nil {
			var r *scenario.Refusal
			if !errors.As(err, &r) {
				t.Fatalf("a refusal that is not a *Refusal: %v", err)
			}
			if len(r.Quote) > scenario.MaxQuoteBytes || r.Err == "" {
				t.Fatalf("refusal %q quotes %d bytes", r.Err, len(r.Quote))
			}
			return
		}
		checkAccepted(t, raw, s)
	})
}

func checkAccepted(t *testing.T, raw []byte, s *scenario.Scenario) {
	t.Helper()
	again, err := scenario.Read("f.json", bytes.Clone(raw))
	if err != nil || !reflect.DeepEqual(s, again) {
		t.Fatalf("an accepted document reads again differently: %v", err)
	}
	outputs := map[int]string{}
	for i, st := range s.Steps {
		if st.Call == nil {
			continue
		}
		if !bytes.Contains(raw, st.Call.Args) {
			t.Fatalf("step %d: arguments %q are not bytes of the input", i, st.Call.Args)
		}
		for _, o := range st.Call.Decided.Obligations {
			if !bytes.Contains(raw, o.Params) {
				t.Fatalf("step %d: parameters %q are not bytes of the input", i, o.Params)
			}
		}
		if _, err := st.Call.Arguments(outputs); err != nil {
			t.Fatalf("step %d: a reference the reader accepted does not substitute: %v", i, err)
		}
		outputs[i] = "${step[0].output}"
	}
}
