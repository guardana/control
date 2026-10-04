//go:build unix

package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/scenario"
)

// swapRounds is how many reads race the swap. The window between a check of
// the path and its open is microseconds wide, so a reader that has one is
// caught within a few hundred reads.
const swapRounds = 5000

// TestReadFileNeverWaitsOnAPipeSwappedIn: a scenario path that a named pipe
// replaces between a check of the path and its open would hold the open for
// ever; whichever file a read finds, it comes back.
func TestReadFileNeverWaitsOnAPipeSwappedIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allowed-read.json")
	regular, pipe := filepath.Join(dir, "regular"), filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}
	var stop atomic.Bool
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		for i := 0; !stop.Load(); i++ {
			if err := os.WriteFile(regular, []byte(doc(plain())), 0o600); err != nil {
				return
			}
			if err := os.Rename(regular, path); err != nil {
				return
			}
			if err := os.Link(pipe, path+".pipe"); err != nil {
				return
			}
			if err := os.Rename(path+".pipe", path); err != nil {
				return
			}
		}
	}()
	defer func() { stop.Store(true); <-swapped }()
	done := make(chan int, 1)
	go func() {
		refused := 0
		for range swapRounds {
			if _, err := scenario.ReadFile(path); err != nil && strings.Contains(err.Error(), "not a regular file") {
				refused++
			}
		}
		done <- refused
	}()
	select {
	case refused := <-done:
		t.Logf("%d of %d reads found the pipe and refused it", refused, swapRounds)
	case <-time.After(30 * time.Second):
		t.Fatal("ReadFile is waiting on a pipe swapped in for the scenario")
	}
}
