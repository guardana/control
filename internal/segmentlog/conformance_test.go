package segmentlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// conformanceDir holds the RecordLog port's cases, each rule with a negative
// control that a kind must also pass.
const conformanceDir = "../../testdata/ports/record-log"

// kinds are every kind the conformance cases run against.
var kinds = []string{KindSegments}

type conformanceCase struct {
	Rule            string `json:"rule"`
	SegmentBytes    int64  `json:"segment_bytes"`
	MaxBytes        int64  `json:"max_bytes"`
	Steps           []step `json:"steps"`
	NegativeControl []step `json:"negative_control"`
}

// step is one call and what it must answer: an append of lines, or of raw
// bytes, a seal, a prune, a reopen, a reopen with other bounds, a locate of
// the segment a line begins, or the bytes retained.
type step struct {
	Append   []string `json:"append"`
	Raw      *string  `json:"raw"`
	Seal     bool     `json:"seal"`
	Prune    int      `json:"prune"`
	Reopen   bool     `json:"reopen"`
	Bounds   []int64  `json:"bounds"`
	Locate   string   `json:"locate"`
	State    string   `json:"state"`
	Retained *int64   `json:"retained"`
	Want     string   `json:"want"`
}

var wanted = map[string]error{"": nil, "budget": ErrBudget, "prune": ErrPrune, "lines": ErrLines}

// TestEveryKindPassesTheConformanceCases runs every case and its negative
// control against every kind. A case with no negative control, or one that
// asks nothing, is refused.
func TestEveryKindPassesTheConformanceCases(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(conformanceDir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no conformance cases in %s: %v", conformanceDir, err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path) //nolint:gosec // G304: the repository's own cases
		if err != nil {
			t.Fatal(err)
		}
		var c conformanceCase
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if c.Rule == "" || len(c.Steps) == 0 || len(c.NegativeControl) == 0 {
			t.Fatalf("%s: a case names its rule, its steps and its negative control", path)
		}
		for _, kind := range kinds {
			t.Run(kind+"/"+filepath.Base(path), func(t *testing.T) {
				runSteps(t, kind, c, c.Steps)
				runSteps(t, kind, c, c.NegativeControl)
			})
		}
	}
}

func runSteps(t *testing.T, kind string, c conformanceCase, steps []step) {
	t.Helper()
	r := &runner{t: t, cfg: config(logDir(t), c.SegmentBytes, c.MaxBytes)}
	r.cfg.Kind = kind
	r.open()
	defer func() { _ = r.l.Close() }()
	for i, s := range steps {
		want, ok := wanted[s.Want]
		if !ok {
			t.Fatalf("step %d: no answer is named %q", i, s.Want)
		}
		if got := r.apply(i, s); !errors.Is(got, want) || (want == nil && got != nil) {
			t.Errorf("step %d: %v, want %v", i, got, want)
		}
	}
}

// runner is one kind's log under the steps of a case.
type runner struct {
	t   *testing.T
	cfg Config
	l   RecordLog
}

func (r *runner) open() {
	r.t.Helper()
	l, err := Open(r.cfg)
	if err != nil {
		r.t.Fatal(err)
	}
	r.l = l
}

// apply takes one step and returns what its call answered; a locate and a
// retained step check their own answer.
func (r *runner) apply(i int, s step) error {
	r.t.Helper()
	switch {
	case s.Append != nil:
		return r.l.Append(context.Background(), []byte(joinLines(s.Append...)))
	case s.Raw != nil:
		return r.l.Append(context.Background(), []byte(*s.Raw))
	case s.Seal:
		return r.l.Seal()
	case s.Prune != 0:
		return r.l.Prune(s.Prune)
	case s.Reopen || s.Bounds != nil:
		r.reopen(s.Bounds)
	default:
		r.check(i, s)
	}
	return nil
}

// reopen closes the log and opens it again, with other bounds when given.
func (r *runner) reopen(bounds []int64) {
	r.t.Helper()
	if bounds != nil {
		r.cfg.SegmentBytes, r.cfg.MaxBytes = bounds[0], bounds[1]
	}
	if err := r.l.Close(); err != nil {
		r.t.Fatal(err)
	}
	r.open()
}

// check takes a step that asks what the log answers.
func (r *runner) check(i int, s step) {
	r.t.Helper()
	switch {
	case s.Locate != "":
		if got := r.l.Locate(sum(s.Locate)).State().String(); got != s.State {
			r.t.Errorf("step %d: %s is located %s, want %s", i, s.Locate, got, s.State)
		}
	case s.Retained != nil:
		if got := r.l.Manifest().Retained; got != *s.Retained {
			r.t.Errorf("step %d: %d bytes retained, want %d", i, got, *s.Retained)
		}
	default:
		r.t.Fatalf("step %d asks nothing", i)
	}
}
