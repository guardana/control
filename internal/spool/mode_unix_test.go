//go:build unix

package spool

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/files"
)

// TestTheSpoolCreatesItsFilesForItsOwnerOnly: with no umask to take bits
// away, the segment and the quarantine are created readable and writable by
// the spool's account alone.
func TestTheSpoolCreatesItsFilesForItsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	shipQuarantined(t, dir)
	for _, name := range []string{segmentFileName(1), quarantineName} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s was created mode %04o, want 0600", name, perm)
		}
	}
}

// TestAFileOthersCanWriteIsRefused: a segment or a quarantine the group or
// others may write can hold what another account put there, so Open refuses
// it; read bits alone take nothing from the evidence and are accepted.
func TestAFileOthersCanWriteIsRefused(t *testing.T) {
	for _, name := range []string{segmentFileName(1), quarantineName} {
		for mode, refused := range map[fs.FileMode]bool{0o620: true, 0o602: true, 0o666: true, 0o644: false, 0o600: false} {
			dir := withQuarantine(t)
			if err := os.Chmod(filepath.Join(dir, name), mode); err != nil {
				t.Fatal(err)
			}
			s, err := openIn(dir)
			if err == nil {
				_ = s.Close()
			}
			switch {
			case refused && (!errors.Is(err, ErrForeignFile) || !errors.Is(err, files.ErrMode)):
				t.Errorf("%s mode %04o: Open = %v, want a refusal of the mode", name, mode, err)
			case !refused && err != nil:
				t.Errorf("%s mode %04o: Open = %v, want it taken", name, mode, err)
			}
		}
	}
}

// withQuarantine leaves a closed spool in a new directory holding one
// segment and a quarantine, and returns the directory.
func withQuarantine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	shipQuarantined(t, dir)
	return dir
}

// shipQuarantined appends one record to a spool in dir opened as it ships,
// quarantines it once delivered, and closes the spool.
func shipQuarantined(t *testing.T, dir string) {
	t.Helper()
	s, err := openIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // a failed close shows in the files the callers check
	if err := s.Append(context.Background(), sample(0)); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reader(Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Quarantine([]*controlv1.Event{sample(0)}); err != nil {
		t.Fatal(err)
	}
}

// TestAFileOfAnotherAccountIsRefused: the directory being the spool's does not
// make a file in it the spool's, so a read of a segment and an append to the
// quarantine each judge the file's own owner.
func TestAFileOfAnotherAccountIsRefused(t *testing.T) {
	s, r := spoolWithDelivered(t, t.TempDir(), 1, nil)
	effectiveUID = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { effectiveUID = os.Geteuid })
	if _, err := r.Quarantine([]*controlv1.Event{sample(0)}); !errors.Is(err, ErrForeignFile) || !errors.Is(err, files.ErrOwner) {
		t.Errorf("Quarantine into a file of another account = %v, want a refusal of the owner", err)
	}
	other, err := s.Reader(Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Next(context.Background()); !errors.Is(err, ErrForeignFile) || !errors.Is(err, files.ErrOwner) {
		t.Errorf("Next from a segment of another account = %v, want a refusal of the owner", err)
	}
}
