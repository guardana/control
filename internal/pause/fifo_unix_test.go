//go:build unix

package pause_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/pause"
)

// TestANamedPipeIsUnknownAndNotWaitedOn: the read judges the opened
// descriptor and does not block on a pipe nobody writes.
func TestANamedPipeIsUnknownAndNotWaitedOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan pause.Snapshot, 1)
	go func() { done <- pause.Read(path, at(), interval) }()
	select {
	case s := <-done:
		if s.State() != pause.Unknown || s.Cause() != pause.CauseUnreadable {
			t.Errorf("a named pipe: %s, %q; want unknown, unreadable", s.State(), s.Cause())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read waited on a named pipe")
	}
}
