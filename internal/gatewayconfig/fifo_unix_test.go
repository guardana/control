//go:build unix

package gatewayconfig

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

// TestLoadRefusesAPipeWithoutWaiting: a named pipe at the configuration path
// with no writer would hold a blocking open for ever, before the byte bound
// could apply; the refusal has to come back instead.
func TestLoadRefusesAPipeWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Load(path, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, files.ErrNotRegular) {
			t.Errorf("Load(a pipe) = %v, want a refusal of a file that is not regular", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load is waiting on a pipe with no writer")
	}
}
