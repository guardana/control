package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

const (
	proposed = controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED
	decided  = controlv1.EventKind_EVENT_KIND_POLICY_DECIDED
	blocked  = controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED
)

func encoded(t *testing.T, events ...*controlv1.Event) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := evidence.EncodeJSONL(&b, events); err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return b.Bytes()
}

func writeTrail(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trail.jsonl")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("writing the trail: %v", err)
	}
	return path
}

// TestATrailIsReadInTheOrderOfItsLinks: events that reached the file out of
// order come back in link order, a repeated line counts once, and the bytes
// after the last newline are not read.
func TestATrailIsReadInTheOrderOfItsLinks(t *testing.T) {
	events := trailOf("r1", proposed, decided, blocked)
	other := trailOf("r2", proposed)
	raw := slices.Concat(encoded(t, events[2], other[0], events[0]), encoded(t, events[0], events[1]), []byte(`{"event_id":`))
	read, err := readTrail(writeTrail(t, raw), false)
	if err != nil {
		t.Fatalf("readTrail: %v", err)
	}
	trail, err := read.trail("r1")
	if err != nil {
		t.Fatalf("trail: %v", err)
	}
	if got := kindList(kindsOf(trail)); got != "[ACTION_PROPOSED, POLICY_DECIDED, ACTION_BLOCKED]" || read.events != 4 {
		t.Errorf("kinds %s, %d distinct events; want the linked three and 4", got, read.events)
	}
	if got := read.requests(); len(got) != 2 || !got["r1"] || !got["r2"] {
		t.Errorf("requests %v", got)
	}
	if trail, err := read.trail("r9"); err != nil || trail != nil {
		t.Errorf("a request with no trail: %v, %v", trail, err)
	}
}

// TestATrailThatCannotBeComparedIsRefused: a broken chain and one request in
// two projects are errors.
func TestATrailThatCannotBeComparedIsRefused(t *testing.T) {
	gap := trailOf("r1", proposed, decided, blocked)
	read, err := readTrail(writeTrail(t, encoded(t, gap[0], gap[2])), false)
	if err != nil {
		t.Fatalf("readTrail: %v", err)
	}
	if _, err := read.trail("r1"); err == nil || !strings.Contains(err.Error(), "is failed") {
		t.Errorf("a trail with a gap: err = %v", err)
	}
	twice := trailOf("r1", proposed)
	elsewhere := trailOf("r1", proposed)
	elsewhere[0].ProjectId, elsewhere[0].EventId = "p2", "other"
	if read, err = readTrail(writeTrail(t, encoded(t, twice[0], elsewhere[0])), false); err != nil {
		t.Fatalf("readTrail: %v", err)
	}
	if _, err := read.trail("r1"); err == nil || !strings.Contains(err.Error(), "in 2 projects") {
		t.Errorf("one request in two projects: err = %v", err)
	}
}

// TestATrailFileThatCannotBeReadIsRefused: a file absent where it must be
// there, a directory and a file past the bound are errors; a file absent
// before the first step holds nothing.
func TestATrailFileThatCannotBeReadIsRefused(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "none.jsonl")
	if read, err := readTrail(absent, true); err != nil || read.events != 0 || len(read.requests()) != 0 {
		t.Errorf("an absent file before the first step: %+v, %v", read, err)
	}
	if _, err := readTrail(absent, false); err == nil {
		t.Error("an absent file after a call was read as empty")
	}
	if _, err := readTrail(t.TempDir(), true); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory: err = %v", err)
	}
	large := writeTrail(t, nil)
	if err := os.Truncate(large, maxTrailBytes+1); err != nil {
		t.Fatalf("growing the file: %v", err)
	}
	if _, err := readTrail(large, true); err == nil || !strings.Contains(err.Error(), "is over") {
		t.Errorf("a file past the bound: err = %v", err)
	}
}
