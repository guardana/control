//go:build unix

package trailfile

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestANamedPipeIsRefusedWithoutWaiting: neither the writer nor the reader
// waits for the other end of a pipe at the file's name.
func TestANamedPipeIsRefusedWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trail.jsonl")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan [2]error, 1)
	go func() {
		w, werr := Open(path)
		if w != nil {
			_ = w.Close()
		}
		_, rerr := ReadFile(path, DefaultMaxLines)
		done <- [2]error{werr, rerr}
	}()
	select {
	case errs := <-done:
		if !errors.Is(errs[0], ErrNotRegular) || !errors.Is(errs[1], ErrNotRegular) {
			t.Errorf("Open, ReadFile of a pipe = %v, %v; want ErrNotRegular", errs[0], errs[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a named pipe waited for its other end")
	}
}
