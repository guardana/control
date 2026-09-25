//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package pause

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestALockIsTakenOnTheJudgedDirectory: a lock held on the judged directory
// refuses a writer whose path was swapped for another directory once the root
// was opened. A writer that took its lock by path would lock the directory
// swapped in and write past the lock that is held.
func TestALockIsTakenOnTheJudgedDirectory(t *testing.T) {
	path := ownPauseFile(t)
	held, err := os.Open(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	swapped := swapAt(t, stepRooted, path, decoyDir(t, path, globalBody))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err = Remove(ctx, path, "g")
	if !*swapped {
		t.Fatal("the write never reached the step, so this case examined nothing")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("Remove = %v, want ErrLocked: the lock was not taken on the judged directory", err)
	}
}
