package runs_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/runs"
)

// TestLookupReadsARunWithoutItsSecret: a root, a child under it and a closed
// run read back as the operator opened them, with no secret's hash.
func TestLookupReadsARunWithoutItsSecret(t *testing.T) {
	_, a, p := setup(t)
	_, rootToken := openRoot(t, a)
	root := idOf(t, rootToken)
	_, childToken := openUnder(t, a, root, alice)
	child := idOf(t, childToken)
	_, closedToken := openRoot(t, a)
	closed := idOf(t, closedToken)
	closedAt := opened.Add(time.Minute)
	if err := a.CloseRun(t.Context(), closed, closedAt); err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2026, time.March, 1, 13, 0, 0, 0, time.UTC)
	for _, want := range []runs.Record{
		{ID: root, Who: alice, Root: root, OpenedAt: opened, ExpiresAt: expires},
		{ID: child, Who: alice, Root: root, Parent: root, OpenedAt: opened, ExpiresAt: expires},
		{ID: closed, Who: alice, Root: closed, OpenedAt: opened, ExpiresAt: expires, ClosedAt: closedAt},
	} {
		got, err := p.Lookup(t.Context(), want.ID)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", want.ID, err)
		}
		if got != want {
			t.Errorf("Lookup(%s) = %+v, want %+v", want.ID, got, want)
		}
	}
}

// TestLookupRefusesWhatItCannotRead: a run no record names, a malformed id, a
// record a writer of the directory broke and a handle nobody opened or one
// closed are refused, each with its own error.
func TestLookupRefusesWhatItCannotRead(t *testing.T) {
	dir, a, p := setup(t)
	_, token := openRoot(t, a)
	broken := idOf(t, token)
	rewrite(t, dir, broken+".run.json", func(m map[string]json.RawMessage) { m["expires_at"] = json.RawMessage(`17`) })
	closedPlane, err := runs.OpenPlane(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := closedPlane.Close(); err != nil {
		t.Fatal(err)
	}
	var none *runs.Plane
	for _, c := range []struct {
		name  string
		plane *runs.Plane
		id    string
		want  error
	}{
		{"a run no record names", p, "run-0123456789abcdef0123456789abcdef", runs.ErrNoRun},
		{"not a run id", p, "run-1", runs.ErrRunID},
		{"a broken record", p, broken, runs.ErrMalformed},
		{"a nil handle", none, broken, runs.ErrClosed},
		{"a closed handle", closedPlane, broken, runs.ErrClosed},
	} {
		if got, err := c.plane.Lookup(t.Context(), c.id); !errors.Is(err, c.want) || got != (runs.Record{}) {
			t.Errorf("%s: Lookup = %+v, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// TestLookupChangesNothing: a lookup leaves every file of the directory, its
// bytes and its time, as it was, and creates none.
func TestLookupChangesNothing(t *testing.T) {
	dir, a, p := setup(t)
	_, token := openRoot(t, a)
	before := snapshotDir(t, dir)
	for range 3 {
		if _, err := p.Lookup(t.Context(), idOf(t, token)); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Lookup(t.Context(), "run-0123456789abcdef0123456789abcdef"); !errors.Is(err, runs.ErrNoRun) {
			t.Fatal(err)
		}
	}
	after := snapshotDir(t, dir)
	if len(after) != len(before) {
		t.Fatalf("the directory held %d files, now %d", len(before), len(after))
	}
	for name, was := range before {
		if after[name] != was {
			t.Errorf("%s changed: %+v, now %+v", name, was, after[name])
		}
	}
}

type fileSeen struct {
	body    string
	modTime time.Time
}

func snapshotDir(t *testing.T, dir string) map[string]fileSeen {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]fileSeen{}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path) //nolint:gosec // G304: a file under the test's own directory
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = fileSeen{string(body), info.ModTime()}
	}
	return out
}
