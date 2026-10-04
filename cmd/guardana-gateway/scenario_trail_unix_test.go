//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// swapRounds is how many reads race the swap. The window between a check of
// the path and its open is microseconds wide, so a reader that has one is
// caught within a few hundred reads.
const swapRounds = 5000

// swapInPipe replaces the file at path, over and over, by a regular file
// holding body and by a named pipe, until the test ends.
func swapInPipe(t *testing.T, path string, body []byte) {
	t.Helper()
	dir := t.TempDir()
	regular, pipe := filepath.Join(dir, "regular"), filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}
	var stop atomic.Bool
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		for !stop.Load() {
			if os.WriteFile(regular, body, 0o600) != nil || os.Rename(regular, path) != nil ||
				os.Link(pipe, path+".pipe") != nil || os.Rename(path+".pipe", path) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { stop.Store(true); <-swapped })
}

// TestTheTrailReaderNeverWaitsOnAPipeSwappedIn: a trail path that a named
// pipe replaces between a check of the path and its open would hold the open
// for ever; whichever file a read finds, it comes back.
func TestTheTrailReaderNeverWaitsOnAPipeSwappedIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trail.jsonl")
	swapInPipe(t, path, nil)
	done := make(chan int, 1)
	go func() {
		refused := 0
		for range swapRounds {
			if _, err := readTrail(path, true); err != nil && strings.Contains(err.Error(), "not a regular file") {
				refused++
			}
		}
		done <- refused
	}()
	select {
	case refused := <-done:
		t.Logf("%d of %d reads found the pipe and refused it", refused, swapRounds)
	case <-time.After(30 * time.Second):
		t.Fatal("the trail reader is waiting on a pipe swapped in for the trail file")
	}
}
