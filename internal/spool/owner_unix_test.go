//go:build unix

package spool

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/files"
)

func openIn(dir string) (*Spool, error) {
	return Open(Options{Dir: dir, MaxBytes: 1 << 20, SegmentBytes: 1 << 16})
}

// TestAnEvidenceDirectoryOthersCanWriteIsRefused: another account that can
// write the directory can plant a segment the spool would ship as evidence.
func TestAnEvidenceDirectoryOthersCanWriteIsRefused(t *testing.T) {
	for _, mode := range []fs.FileMode{0o770, 0o720, 0o707, 0o777} {
		dir := t.TempDir()
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		s, err := openIn(dir)
		if err == nil {
			_ = s.Close()
		}
		if !errors.Is(err, ErrInvalidOptions) || !errors.Is(err, files.ErrMode) {
			t.Errorf("mode %04o: Open = %v, want a refusal of the mode", mode, err)
		}
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: a directory others may read and not write, which the spool takes
		t.Fatal(err)
	}
	s, err := openIn(dir)
	if err != nil {
		t.Fatalf("a directory only its owner writes: %v", err)
	}
	_ = s.Close()
}

// TestAnEvidenceDirectoryAnotherAccountOwnsIsRefused.
func TestAnEvidenceDirectoryAnotherAccountOwnsIsRefused(t *testing.T) {
	effectiveUID = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { effectiveUID = os.Geteuid })
	s, err := openIn(t.TempDir())
	if err == nil {
		_ = s.Close()
	}
	if !errors.Is(err, ErrInvalidOptions) || !errors.Is(err, files.ErrOwner) {
		t.Fatalf("Open = %v, want a refusal of the owner", err)
	}
}

// TestALinkAtTheFirstSegmentIsNeverWrittenThrough: a link put where the
// spool would create its first segment is refused, and the file it points at
// is never made.
func TestALinkAtTheFirstSegmentIsNeverWrittenThrough(t *testing.T) {
	dir, victim := t.TempDir(), filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(victim, segmentPath(dir, 1)); err != nil {
		t.Fatal(err)
	}
	s, err := openIn(dir)
	if err == nil {
		err = s.Append(context.Background(), sample(1))
		_ = s.Close()
	}
	if err == nil {
		t.Error("the spool appended beside a link at its first segment")
	}
	if _, statErr := os.Lstat(victim); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the link's target exists: %v", statErr)
	}
}

// TestALinkAtTheLastSegmentIsNeverWrittenThrough: the last segment swapped
// for a link to a copy of itself is refused when the spool reopens it to
// append, and the copy is left as it was.
func TestALinkAtTheLastSegmentIsNeverWrittenThrough(t *testing.T) {
	dir := t.TempDir()
	seg, body := oneSegment(t, dir)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(seg); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, seg); err != nil {
		t.Fatal(err)
	}
	s, err := openIn(dir)
	if err == nil {
		err = s.Append(context.Background(), sample(2))
		_ = s.Close()
	}
	if err == nil {
		t.Error("the spool appended to a segment that is a link")
	}
	after, readErr := os.ReadFile(victim) //nolint:gosec // G304: the test's own file
	if readErr != nil || len(after) != len(body) {
		t.Errorf("the link's target holds %d bytes after, %d before: %v", len(after), len(body), readErr)
	}
}

// oneSegment writes one record into a spool in dir and returns its segment's
// path and bytes.
func oneSegment(t *testing.T, dir string) (string, []byte) {
	t.Helper()
	s, err := openIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(context.Background(), sample(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	seg := segmentPath(dir, 1)
	body, err := os.ReadFile(seg) //nolint:gosec // G304: the test's own spool
	if err != nil {
		t.Fatal(err)
	}
	return seg, body
}

// TestTheSegmentOpenRefusesALink: what Open checked can be swapped for a link
// before an append reopens it, so the open itself refuses one, whether its
// target exists or not.
func TestTheSegmentOpenRefusesALink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openOSFile(link); err == nil {
		_ = f.Close()
		t.Error("a dangling link was opened")
	}
	if err := os.WriteFile(victim, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := openOSFile(link); err == nil {
		_ = f.Close()
		t.Error("a link to a file was opened")
	}
	if body, err := os.ReadFile(victim); err != nil || string(body) != "kept" { //nolint:gosec // G304: the test's own file
		t.Errorf("the target holds %q: %v", body, err)
	}
}
