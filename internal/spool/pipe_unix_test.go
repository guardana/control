//go:build unix && !(aix || illumos || solaris)

package spool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// pipeAt replaces the file at path with a named pipe.
func pipeAt(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// within runs call and fails the test when it waits on pipe for long. Both
// ends opened at once release an open that waits, so the spool's lock is free
// again for the cleanup.
func within(t *testing.T, pipe string, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		if f, err := os.OpenFile(pipe, os.O_RDWR, 0); err == nil { //nolint:gosec // G304: the test's own pipe
			defer f.Close() //nolint:errcheck // a pipe the test made
		}
		<-done
		t.Fatal("the open waited on a named pipe")
		return nil
	}
}

// TestAPipeAtASegmentNameIsNotReadFrom: a named pipe put at a segment's name
// between the look at its entry and the open would hold an open for read until
// a writer comes; it is refused at once.
func TestAPipeAtASegmentNameIsNotReadFrom(t *testing.T) {
	dir := t.TempDir()
	pipe := firstSegmentPath(dir)
	var armed atomic.Bool
	s, err := Open(Options{Dir: dir, MaxBytes: 1 << 20, SegmentBytes: 1, openRead: func(root *os.Root, name string, flag int) (*os.File, error) {
		if armed.CompareAndSwap(true, false) && filepath.Join(dir, name) == pipe {
			pipeAt(t, pipe)
		}
		return root.OpenFile(name, flag, 0)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // the assertion is below
	for i := 1; i <= 2; i++ {
		if err := s.Append(context.Background(), sample(i)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Reader(Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	err = within(t, pipe, func() error {
		_, _, err := r.Next(context.Background())
		return err
	})
	if !errors.Is(err, ErrForeignFile) {
		t.Errorf("Next = %v, want ErrForeignFile", err)
	}
}

// TestAPipeAtASegmentNameIsNotWrittenTo: the same pipe would hold an open for
// write until a reader comes; it is refused at once.
func TestAPipeAtASegmentNameIsNotWrittenTo(t *testing.T) {
	dir := t.TempDir()
	pipe, _ := oneSegment(t, dir)
	var armed atomic.Bool
	s, err := Open(Options{Dir: dir, MaxBytes: 1 << 20, SegmentBytes: 1 << 16, openFile: func(root *os.Root, name string, flag int) (segmentFile, error) {
		if armed.CompareAndSwap(true, false) && filepath.Join(dir, name) == pipe {
			pipeAt(t, pipe)
		}
		return openOSFile(root, name, flag)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // the assertion is below
	armed.Store(true)
	if err := within(t, pipe, func() error { return s.Append(context.Background(), sample(2)) }); err == nil {
		t.Error("Append wrote to a named pipe")
	}
}
