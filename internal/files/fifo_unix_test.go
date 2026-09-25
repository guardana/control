//go:build unix

package files_test

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

// TestReadRegularRefusesAPipeWithoutWaiting: a named pipe with no writer
// would hold a blocking open for ever; the refusal has to come back instead.
func TestReadRegularRefusesAPipeWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := files.ReadRegular(path, 16, 0o077)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, files.ErrNotRegular) {
			t.Errorf("ReadRegular(a pipe) = %v, want ErrNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadRegular is waiting on a pipe with no writer")
	}
}
