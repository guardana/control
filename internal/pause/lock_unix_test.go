//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package pause_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/pause"
)

// holdLock takes the lock a writer takes, as another process would, and
// returns what releases it.
func holdLock(t *testing.T, dir string) func() {
	t.Helper()
	d, err := os.Open(dir) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return func() { _ = d.Close() }
}

// TestAWriterWaitsForTheLockUntilItsContextEnds: a lock held elsewhere keeps
// a writer out, and the writer says so rather than write around it.
func TestAWriterWaitsForTheLockUntilItsContextEnds(t *testing.T) {
	path := emptyDir(t)
	if err := pause.Init(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	release := holdLock(t, filepath.Dir(path))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := pause.Add(ctx, path, entry("p1", pause.Scope{Kind: "global"})); !errors.Is(err, pause.ErrLocked) {
		t.Errorf("Add under a held lock = %v, want ErrLocked", err)
	}
	if got := ids(t, path); len(got) != 0 {
		t.Errorf("a writer kept out wrote %v", got)
	}
	release()
	if err := pause.Add(context.Background(), path, entry("p1", pause.Scope{Kind: "global"})); err != nil {
		t.Errorf("Add after the lock was released: %v", err)
	}
}
