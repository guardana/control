//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

// TestAJournalOnlyOpensItsOwnFile: the journal holds what each call carried,
// so it is opened only as a file of one name that this account owns and no
// other account may read or write; a link or a pipe at its name is refused.
func TestAJournalOnlyOpensItsOwnFile(t *testing.T) {
	t.Parallel()
	for name, prepare := range map[string]func(t *testing.T, path string){
		"a link to a file of this account": func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere")
			mustWrite(t, target, 0o600)
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"a file with a second name": func(t *testing.T, path string) {
			mustWrite(t, path, 0o600)
			if err := os.Link(path, filepath.Join(t.TempDir(), "second")); err != nil {
				t.Fatal(err)
			}
		},
		"a file its group may read":   func(t *testing.T, path string) { mustWrite(t, path, 0o640) },
		"a file its group may write":  func(t *testing.T, path string) { mustWrite(t, path, 0o620) },
		"a file anyone may read":      func(t *testing.T, path string) { mustWrite(t, path, 0o604) },
		"a file anyone may write":     func(t *testing.T, path string) { mustWrite(t, path, 0o602) },
		"a directory":                 func(t *testing.T, path string) { mustMkdir(t, path) },
		"a named pipe with a reader":  func(t *testing.T, path string) { mustReadPipe(t, path) },
		"a named pipe with no reader": func(t *testing.T, path string) { mustMkfifo(t, path) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			prepare(t, filepath.Join(dir, "web.jsonl"))
			if err := openWithin(t, dir); err == nil {
				t.Fatalf("the journal opened %s", name)
			}
		})
	}
}

// TestAJournalReopensItsOwnFile: a journal file of this account that only it
// may read and write is appended to.
func TestAJournalReopensItsOwnFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "web.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := openJournal(dir, "web")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.record(entry{Call: "fetch_page"}); err != nil {
		t.Fatal(err)
	}
	if err := j.close(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "{}\n"+`{"call":"fetch_page"}`+"\n" { //nolint:gosec // G304: a file under the test's own directory
		t.Fatalf("the journal file holds %q, %v", raw, err)
	}
}

// TestAJournalOfAnotherAccountIsRefused: a file only its owner may reach is
// still refused as the journal of an account that does not own it.
func TestAJournalOfAnotherAccountIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "web.jsonl"), 0o600)
	if _, err := openJournalOf(dir, "web", os.Geteuid()+1); !errors.Is(err, files.ErrOwner) {
		t.Fatalf("a file of uid %d opened as the journal of uid %d: %v", os.Geteuid(), os.Geteuid()+1, err)
	}
}

// openWithin opens the web journal in dir, and fails the test when the open
// waits instead of answering.
func openWithin(t *testing.T, dir string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		j, err := openJournal(dir, "web")
		if err == nil {
			err = j.close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("opening the journal is still waiting")
		return nil
	}
}

func mustWrite(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustMkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// mustReadPipe makes a named pipe at path and holds its reading end open
// until the test ends, so an open for writing does not fail on its own.
func mustReadPipe(t *testing.T, path string) {
	t.Helper()
	mustMkfifo(t, path)
	r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: a pipe under the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
}
