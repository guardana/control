//go:build unix

package policykey_test

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policykey"
)

// TestReadPrivateRefusesAPipe: `--key` pointed at a named pipe with no writer
// is refused at once rather than waiting for a key to be written into it.
func TestReadPrivateRefusesAPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := policykey.ReadPrivate(path)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, files.ErrNotRegular) {
			t.Errorf("ReadPrivate(a pipe) = %v, want ErrNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadPrivate is waiting on a pipe with no writer")
	}
}
