package evidence_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/guardana/control/internal/evidence"
)

// consumerFacts is the file the evidence-report example reads in place of
// this package, which a module of its own cannot import. The file sits in
// this repository only: a program that requires this module does not hold
// it, and this test fails there rather than pass over nothing.
const consumerFacts = "../../examples/evidence-report/testdata/plane.json"

type planeFacts struct {
	LineBoundBytes int `json:"line_bound_bytes"`
	Chain          struct {
		States          []string    `json:"states"`
		Edges           []chainEdge `json:"edges"`
		UnplaceableKind int32       `json:"unplaceable_kind"`
	} `json:"chain"`
}

type chainEdge struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

func readConsumerFacts(t *testing.T) planeFacts {
	t.Helper()
	b, err := os.ReadFile(consumerFacts)
	if err != nil {
		t.Fatalf("the example's facts: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f planeFacts
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("%s: %v", consumerFacts, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("%s holds more than one JSON value", consumerFacts)
	}
	if len(f.Chain.States) == 0 || len(f.Chain.Edges) == 0 {
		t.Fatalf("%s names %d states and %d edges: a check against it would examine nothing",
			consumerFacts, len(f.Chain.States), len(f.Chain.Edges))
	}
	return f
}

func TestConsumerLineBoundIsTheDecoders(t *testing.T) {
	if got := readConsumerFacts(t).LineBoundBytes; got != evidence.MaxLineBytes {
		t.Fatalf("%s bounds a line at %d bytes, evidence.MaxLineBytes at %d", consumerFacts, got, evidence.MaxLineBytes)
	}
}

// TestConsumerChainIsTheValidators holds the example's chain to ChainSteps
// edge for edge: the states in order, every step taken with where it goes,
// and the kind number no build declares.
func TestConsumerChainIsTheValidators(t *testing.T) {
	facts := readConsumerFacts(t).Chain
	states, want := validatorChain(t, facts.UnplaceableKind)
	if !slices.Equal(states, facts.States) {
		t.Errorf("states:\n%q\nthe example's file:\n%q", states, facts.States)
	}
	listed := map[chainEdge]bool{}
	for _, e := range facts.Edges {
		switch {
		case listed[e]:
			t.Errorf("the example's file lists %+v twice", e)
		case !want[e]:
			t.Errorf("the example's file lists %+v, a step the validator does not take", e)
		}
		listed[e] = true
	}
	for e := range want {
		if !listed[e] {
			t.Errorf("the validator takes %+v, which the example's file does not list", e)
		}
	}
}

// validatorChain returns the states ChainSteps names, in order, and the steps
// it takes. It fails unless every state reads the one undeclared kind it
// lists, and reads it as the kind number unplaceable, unplaced.
func validatorChain(t *testing.T, unplaceable int32) ([]string, map[chainEdge]bool) {
	t.Helper()
	var states []string
	taken := map[chainEdge]bool{}
	unplaced := 0
	for _, s := range evidence.ChainSteps() {
		if !slices.Contains(states, s.From) {
			states = append(states, s.From)
		}
		switch {
		case !s.Declared:
			unplaced++
			if !s.Unplaceable || int32(s.Kind) != unplaceable {
				t.Errorf("the validator reads kind %d after %s as unplaceable %t; the example's file names kind %d",
					int32(s.Kind), s.From, s.Unplaceable, unplaceable)
			}
		case s.Allowed:
			taken[chainEdge{From: s.From, Kind: s.Kind.String(), To: s.To}] = true
		}
	}
	if unplaced != len(states) {
		t.Errorf("the validator lists %d steps of an undeclared kind over %d states, want one each", unplaced, len(states))
	}
	return states, taken
}
