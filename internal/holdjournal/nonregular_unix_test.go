//go:build unix

package holdjournal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/holdjournal"
)

// answerWait is how long a pass gets before it is called stuck. It is far
// longer than any pass over a handful of small files takes, and far shorter
// than the test binary's own timeout, so a pass waiting on a named pipe is
// reported as this failure and not as a run nobody can read.
const answerWait = 5 * time.Second

// answersWithin runs call on its own goroutine and fails the test when it does
// not answer. The goroutine is left where it is on that path: it holds the
// journal's mutex, so waiting for it or closing the journal would turn a named
// failure into a stuck run.
func answersWithin(t *testing.T, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(answerWait):
		t.Fatalf("no answer within %v: the call is waiting in open(2) on a file that is not a regular file", answerWait)
		return nil
	}
}

// makePipe puts a named pipe where path is, over whatever is there.
func makePipe(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clearing %q: %v", path, err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("making a named pipe at %q: %v", path, err)
	}
}

// TestAFileThatIsNotARegularFileRefusesThePassInsteadOfWaiting: a pass that
// opened the pipe would wait for a writer that never comes, under the journal's
// mutex, so the whole plane wedges before it serves. What a name says a file is
// never decides whether it is opened, and the journal writes regular files
// alone, so everything else under an entry's name is somebody else's.
func TestAFileThatIsNotARegularFileRefusesThePassInsteadOfWaiting(t *testing.T) {
	put := map[string]func(t *testing.T, path string){
		"a named pipe": makePipe,
		"a symbolic link to a file the journal did not write": func(t *testing.T, path string) {
			t.Helper()
			outside := filepath.Join(t.TempDir(), "elsewhere")
			writeFile(t, outside, []byte("not an entry"))
			if err := os.Remove(path); err != nil {
				t.Fatalf("clearing %q: %v", path, err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatalf("linking %q: %v", path, err)
			}
		},
		"a directory": func(t *testing.T, path string) {
			t.Helper()
			if err := os.Remove(path); err != nil {
				t.Fatalf("clearing %q: %v", path, err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatalf("making a directory at %q: %v", path, err)
			}
		},
	}
	for name, place := range put {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			j, err := holdjournal.Open(dir)
			if err != nil {
				t.Fatalf("opening the journal: %v", err)
			}
			record(t, j, "req-1")
			record(t, j, "req-foreign")
			place(t, entryFileOf(t, dir, "req-foreign"))

			var listing holdjournal.Listing
			err = answersWithin(t, func() error {
				var listErr error
				listing, listErr = j.List(context.Background(), 8)
				return listErr
			})
			if !errors.Is(err, holdjournal.ErrForeignFile) {
				t.Fatalf("listing over %s: %v, want ErrForeignFile", name, err)
			}
			if listing.Complete || len(listing.Held) != 0 {
				t.Fatalf("a refused pass reported %+v", listing)
			}
			if err := j.Close(); err != nil {
				t.Fatalf("closing the journal: %v", err)
			}
		})
	}
}

// TestANamedPipeUnderAnEntryNameRefusesAFlipInsteadOfWaiting: a flip finds its
// entry by name and reads no directory, so the listing's check cannot help it
// and the open itself has to refuse what is not a regular file. The same open
// is what a pass takes after the listing, which is where a pipe put in place
// between the two would otherwise be waited on.
func TestANamedPipeUnderAnEntryNameRefusesAFlipInsteadOfWaiting(t *testing.T) {
	dir := newDir(t)
	j, err := holdjournal.Open(dir)
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	record(t, j, "req-pipe")
	makePipe(t, entryFileOf(t, dir, "req-pipe"))

	err = answersWithin(t, func() error {
		return j.Mark(context.Background(), "req-pipe", holdjournal.StateClosing)
	})
	if !errors.Is(err, holdjournal.ErrForeignFile) {
		t.Fatalf("flipping an entry that is a named pipe: %v, want ErrForeignFile", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
}

// TestANamedPipeUnderTheMarkersNameRefusesTheOpenInsteadOfWaiting: the same
// wait, one step earlier, where it would stop a plane from ever reaching its
// first hold.
func TestANamedPipeUnderTheMarkersNameRefusesTheOpenInsteadOfWaiting(t *testing.T) {
	dir := newDir(t)
	makePipe(t, filepath.Join(dir, "journal.meta"))

	for name, open := range map[string]func(string, ...holdjournal.Option) (*holdjournal.Journal, error){
		"a plane":  holdjournal.Open,
		"a reader": holdjournal.OpenReadOnly,
	} {
		t.Run(name, func(t *testing.T) {
			var j *holdjournal.Journal
			err := answersWithin(t, func() error {
				var openErr error
				j, openErr = open(dir)
				return openErr
			})
			if !errors.Is(err, holdjournal.ErrNotAJournal) {
				t.Fatalf("opening a directory whose marker is a named pipe: %v, want ErrNotAJournal", err)
			}
			if j != nil {
				t.Fatal("a refused open handed back a journal")
			}
		})
	}
}
