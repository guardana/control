//go:build linux || darwin

package files_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

// flocked takes an exclusive flock of path, as another writer of this
// package or a caller holding its directory does, and returns its release.
func flocked(t *testing.T, path string) func() {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = f.Close()
		}
	}
	t.Cleanup(release)
	return release
}

// TestAWriterThatWaitedOnTheLockFindsTheNameChanged: while another writer
// holds the judged file's lock, the replace waits; that writer puts its own
// file at the name and lets go, and the replace is refused with nothing
// written, the other writer's file at the name.
func TestAWriterThatWaitedOnTheLockFindsTheNameChanged(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	writeFile(t, out, []byte("judged"), 0o600)
	judged := lstat(t, out)
	release := flocked(t, out)
	opened := make(chan struct{})
	files.BeforeJudgedLocked(t, func() { close(opened) })
	done := make(chan error, 1)
	go func() { done <- files.ReplaceJudged(dir, "out", []byte("refused"), 0o644, judged) }()
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("the replace never opened the judged file")
	}
	select {
	case err := <-done:
		t.Fatalf("the replace answered %v while another writer held the judged file's lock", err)
	case <-time.After(200 * time.Millisecond):
	}
	renamedIn(t, dir, "out", "the first writer's")
	release()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the replace still waits once the lock was let go")
	}
	if !errors.Is(err, files.ErrNotJudged) || !errors.Is(err, files.ErrNothingWritten) {
		t.Errorf("a replace whose judged file was replaced while it waited = %v, want ErrNotJudged with nothing written", err)
	}
	if got := readBody(t, out); got != "the first writer's" {
		t.Errorf("the name holds %q, want the first writer's file", got)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Errorf("the directory holds %v, want the one name", got)
	}
}

// TestACallerHoldingItsDirectoryLockStillReplaces: callers hold an exclusive
// flock of the directory while they write; the replace locks the judged
// file, never the directory, so it neither waits on nor refuses that lock.
func TestACallerHoldingItsDirectoryLockStillReplaces(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	writeFile(t, out, []byte("judged"), 0o600)
	judged := lstat(t, out)
	flocked(t, dir)
	done := make(chan error, 1)
	go func() { done <- files.ReplaceJudged(dir, "out", []byte("new"), 0o644, judged) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a replace under the caller's directory lock: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the replace waits on the caller's directory lock")
	}
	if got := readBody(t, out); got != "new" {
		t.Errorf("the name holds %q, want the new file", got)
	}
}
