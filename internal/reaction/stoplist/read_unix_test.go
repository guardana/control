//go:build unix

package stoplist

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// readCase is what a read of one directory must give: the sentinel and the
// cause, or, with neither, the content as written.
type readCase struct {
	name  string
	dir   func(t *testing.T) string
	err   Error
	cause reaction.Cause
}

func checkRead(t *testing.T, c readCase) {
	t.Helper()
	dir := c.dir(t)
	got, err := Read(dir)
	if c.err == "" {
		if err != nil {
			t.Fatalf("%s: Read = %v, want the content", c.name, err)
		}
		want, rerr := os.ReadFile(filepath.Join(dir, FileName)) //nolint:gosec // G304: the test's own temporary file
		if rerr != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: Read returned %d bytes, not the %d written (%v)", c.name, len(got), len(want), rerr)
		}
		return
	}
	if !errors.Is(err, c.err) {
		t.Errorf("%s: Read = %v, want %q", c.name, err, c.err)
	}
	if cause := CauseOf(err); cause != c.cause {
		t.Errorf("%s: CauseOf = %q, want %q", c.name, cause, c.cause)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// TestEachFileRefusalHasItsCause: every way the directory or the file can
// let another account write, or not be the file at all, is refused with its
// own sentinel and cause, and the near miss beside each reads.
func TestEachFileRefusalHasItsCause(t *testing.T) {
	ok := func(t *testing.T) string { return listDir(t, stoppingList(t)) }
	withFileMode := func(m os.FileMode) func(t *testing.T) string {
		return func(t *testing.T) string {
			dir := ok(t)
			chmod(t, filepath.Join(dir, FileName), m)
			return dir
		}
	}
	withDirMode := func(m os.FileMode) func(t *testing.T) string {
		return func(t *testing.T) string {
			dir := ok(t)
			chmod(t, dir, m)
			return dir
		}
	}
	for _, c := range []readCase{
		{name: "owner-only", dir: ok},
		{name: "a file the group may read", dir: withFileMode(0o640)},
		{name: "a directory the group may enter", dir: withDirMode(0o750)},
		{name: "a file the group may write", dir: withFileMode(0o620), err: ErrFileMode, cause: CauseMode},
		{name: "a file others may write", dir: withFileMode(0o602), err: ErrFileMode, cause: CauseMode},
		{name: "a directory the group may write", dir: withDirMode(0o720), err: ErrDirMode, cause: CauseMode},
		{name: "a directory others may write", dir: withDirMode(0o702), err: ErrDirMode, cause: CauseMode},
		{name: "no file", dir: func(t *testing.T) string {
			dir := ok(t)
			if err := os.Remove(filepath.Join(dir, FileName)); err != nil {
				t.Fatal(err)
			}
			return dir
		}, err: ErrMissing, cause: CauseMissing},
		{name: "no directory", dir: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "absent")
		}, err: ErrMissing, cause: CauseMissing},
		{name: "a link at the file", dir: func(t *testing.T) string {
			dir := ok(t)
			path := filepath.Join(dir, FileName)
			if err := os.Rename(path, filepath.Join(dir, "real.jsonl")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real.jsonl", path); err != nil {
				t.Fatal(err)
			}
			return dir
		}, err: ErrLink, cause: CauseLink},
		{name: "a link at the directory", dir: func(t *testing.T) string {
			dir := ok(t)
			link := dir + ".link"
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, err: ErrDirLink, cause: CauseLink},
		{name: "a directory at the file's name", dir: func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "stops")
			if err := os.MkdirAll(filepath.Join(dir, FileName), 0o700); err != nil {
				t.Fatal(err)
			}
			chmod(t, dir, 0o700)
			return dir
		}, err: ErrNotRegular, cause: CauseUnreadable},
	} {
		checkRead(t, c)
	}
}

// TestTheReadIsBoundedAtTheListsBound: a file of exactly the list's bound is
// read whole, one byte more is refused as too large.
func TestTheReadIsBoundedAtTheListsBound(t *testing.T) {
	at := bytes.Repeat([]byte{'x'}, reaction.MaxListBytes)
	checkRead(t, readCase{name: "at the bound", dir: func(t *testing.T) string { return listDir(t, at) }})
	over := append(at, 'x')
	checkRead(t, readCase{name: "one byte over", dir: func(t *testing.T) string { return listDir(t, over) },
		err: ErrTooLarge, cause: reaction.CauseTooLarge})
}

// actAs makes this package judge ownership as the accounts uids name, one per
// check in the order the checks are made, the last repeating.
func actAs(t *testing.T, uids ...int) {
	t.Helper()
	var n atomic.Int64
	effectiveUID = func() int {
		i := int(n.Add(1)) - 1
		return uids[min(i, len(uids)-1)]
	}
	t.Cleanup(func() { effectiveUID = os.Geteuid })
}

// TestAListOwnedByAnotherAccountIsRefused: the directory, then the file, each
// owned by another account than the reader's, while everything else about
// them reads; the same list read as its owner is the control.
func TestAListOwnedByAnotherAccountIsRefused(t *testing.T) {
	me, other := os.Geteuid(), os.Geteuid()+1
	for _, c := range []struct {
		name string
		uids []int
		err  Error
	}{
		{"as its owner", []int{me}, ""},
		{"the directory", []int{other}, ErrDirOwner},
		{"the file", []int{me, other}, ErrFileOwner},
	} {
		t.Run(c.name, func(t *testing.T) {
			actAs(t, c.uids...)
			rc := readCase{name: c.name, dir: func(t *testing.T) string { return listDir(t, stoppingList(t)) }, err: c.err}
			if c.err != "" {
				rc.cause = CauseOwner
			}
			checkRead(t, rc)
		})
	}
}

// TestANamedPipeIsRefusedAndNotWaitedOn: the read judges the opened
// descriptor and does not block on a pipe nobody writes.
func TestANamedPipeIsRefusedAndNotWaitedOn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stops")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, FileName), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Read(dir)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) || CauseOf(err) != CauseUnreadable {
			t.Errorf("a named pipe: %v, want ErrNotRegular, unreadable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read waited on a named pipe")
	}
}
