package trailfile

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// fileChange is something another process does to the file under a writer,
// and what undoes it where it can be undone.
type fileChange struct {
	do, undo func(t *testing.T, path, moved string)
	// left is what the file at its path holds after the change, and moved
	// what the file renamed away holds, "-" where there is none.
	left, moved string
	// also is a refusal the append matches beside ErrChanged.
	also error
}

var fileChanges = map[string]fileChange{
	"removed": {do: func(t *testing.T, path, _ string) { mustDo(t, os.Remove(path)) }, left: "-", moved: "-"},
	"renamed": {
		do:   func(t *testing.T, path, moved string) { mustDo(t, os.Rename(path, moved)) },
		undo: func(t *testing.T, path, moved string) { mustDo(t, os.Rename(moved, path)) },
		left: "-", moved: firstLine,
	},
	"replaced by another file": {
		do: func(t *testing.T, path, moved string) {
			mustDo(t, os.Rename(path, moved))
			writeFile(t, path, "")
		},
		undo: func(t *testing.T, path, moved string) { mustDo(t, os.Rename(moved, path)) },
		left: "", moved: firstLine,
	},
	"replaced by a link to it": {
		do: func(t *testing.T, path, moved string) {
			mustDo(t, os.Rename(path, moved))
			mustDo(t, os.Symlink(moved, path))
		},
		undo: func(t *testing.T, path, moved string) {
			mustDo(t, os.Remove(path))
			mustDo(t, os.Rename(moved, path))
		},
		left: firstLine, moved: firstLine, also: ErrNotRegular,
	},
	"truncated": {
		do:   func(t *testing.T, path, _ string) { mustDo(t, os.Truncate(path, 0)) },
		undo: func(t *testing.T, path, _ string) { writeFile(t, path, firstLine) },
		left: "", moved: "-",
	},
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// holds checks what path holds, "-" being no file at all.
func holds(t *testing.T, what, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the test's own temp file
	switch {
	case want == "-" && !errors.Is(err, os.ErrNotExist):
		t.Errorf("%s: %s is there (%v), want it gone", what, path, err)
	case want != "-" && (err != nil || string(raw) != want):
		t.Errorf("%s: %s holds %q (%v), want %q", what, path, raw, err, want)
	}
}

// TestAFileChangedUnderTheWriterTakesNoMore: a file removed, renamed,
// replaced or truncated under a live writer is refused before a byte is
// written, and the writer takes nothing more even once the change is undone.
// A later append names the change, not a cut that never failed.
func TestAFileChangedUnderTheWriterTakesNoMore(t *testing.T) {
	for name, c := range fileChanges {
		path := trailPath(t)
		moved := path + ".moved"
		w := openWriter(t, path)
		mustAppend(t, w, event(1))
		c.do(t, path, moved)
		err := w.Append(context.Background(), []*controlv1.Event{event(2)})
		if !errors.Is(err, ErrChanged) || (c.also != nil && !errors.Is(err, c.also)) {
			t.Errorf("%s: Append = %v, want ErrChanged %v", name, err, c.also)
		}
		holds(t, name, path, c.left)
		holds(t, name, moved, c.moved)
		if c.undo == nil {
			continue
		}
		c.undo(t, path, moved)
		holds(t, name+", undone", path, firstLine)
		for range 2 {
			if err := w.Append(context.Background(), []*controlv1.Event{event(3)}); !errors.Is(err, ErrChanged) || errors.Is(err, ErrFailed) {
				t.Errorf("%s: an append once the change was undone = %v, want ErrChanged and not ErrFailed", name, err)
			}
		}
		holds(t, name+", undone and appended to", path, firstLine)
	}
}

// TestAFileMovedDuringAnAppendIsNotReported: the file renamed between the
// write and the sync holds the append under another name, and the append is
// not reported written.
func TestAFileMovedDuringAnAppendIsNotReported(t *testing.T) {
	path := trailPath(t)
	w := openWriter(t, path)
	mustAppend(t, w, event(1))
	w.ops = fileOps{write: osOps.write, sync: func(f *os.File) error {
		if err := os.Rename(path, path+".moved"); err != nil {
			return err
		}
		return f.Sync()
	}}
	if err := w.Append(context.Background(), []*controlv1.Event{event(2)}); !errors.Is(err, ErrChanged) {
		t.Errorf("Append = %v, want ErrChanged", err)
	}
	w.ops = osOps
	mustDo(t, os.Rename(path+".moved", path))
	if err := w.Append(context.Background(), []*controlv1.Event{event(3)}); !errors.Is(err, ErrChanged) || errors.Is(err, ErrFailed) {
		t.Errorf("an append after = %v, want ErrChanged and not ErrFailed", err)
	}
}

// TestAFileChangedUnderTheCollectorReleasesNothing drives the plane's real
// exporter against the receiver over a writer whose file was changed: every
// request is answered 503, and the exporter's cursor passes nothing.
func TestAFileChangedUnderTheCollectorReleasesNothing(t *testing.T) {
	for _, name := range []string{"removed", "renamed", "truncated"} {
		t.Run(name, func(t *testing.T) {
			c := fileChanges[name]
			path := trailPath(t)
			w := openWriter(t, path)
			mustAppend(t, w, event(1))
			st, url := serveReceiver(t, w)
			c.do(t, path, path+".moved")
			events := chain("p1", "a", kindProposed, kindDecided, kindBlocked)
			sp := spoolHolding(t, events)
			e, stop := runExporter(t, sp, url)
			within(t, func() bool { return st.count(http.StatusServiceUnavailable) >= 3 })
			stop()
			if ex := e.Stats(); ex.Acknowledged != 0 || ex.Quarantined != 0 || st.count(http.StatusOK) != 0 {
				t.Errorf("the exporter reports %+v and %d answers of 200", ex, st.count(http.StatusOK))
			}
			if stats, err := sp.Stats(); err != nil || stats.Unacknowledged == 0 {
				t.Errorf("the spool reports %+v, %v; want every record kept", stats, err)
			}
			holds(t, name, path, c.left)
			holds(t, name, path+".moved", c.moved)
		})
	}
}
