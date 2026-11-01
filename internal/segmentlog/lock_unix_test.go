//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package segmentlog

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// probeFor holds a shared lock on the log's lock file for d, as a reader
// asking whether a writer holds the directory does for an instant.
func probeFor(t *testing.T, dir string, d time.Duration) <-chan struct{} {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, LockName)) //nolint:gosec // G304: the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(d)
		_ = f.Close()
		close(done)
	}()
	return done
}

// TestAWriterWaitsOutAReadersProbe: a writer opening while a reader holds
// the lock file shared for a moment opens once it lets go; one held past the
// writer's bound is refused as held.
func TestAWriterWaitsOutAReadersProbe(t *testing.T) {
	cfg := config(logDir(t), 16, 32)
	_ = openLog(t, cfg).Close()
	done := probeFor(t, cfg.Dir, 50*time.Millisecond)
	l, err := openSegments(cfg)
	if err != nil {
		t.Fatalf("an open beside a reader's probe: %v", err)
	}
	_ = l.Close()
	<-done
	done = probeFor(t, cfg.Dir, 2*lockTries*lockPause)
	if _, err := openSegments(cfg); !errors.Is(err, ErrLocked) {
		t.Errorf("an open beside a lock held past the bound: %v, want ErrLocked", err)
	}
	<-done
}
