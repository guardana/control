//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	ondisk "github.com/guardana/control/internal/files"
)

// readerLocked takes a shared flock of path on an open of its own, as any
// account that can read the file may, and returns its release.
func readerLocked(t *testing.T, path string) func() {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = f.Close()
		}
	}
	t.Cleanup(release)
	return release
}

// shortLockWaits shortens every wait a command makes for a lock, for the
// test's length.
func shortLockWaits(t *testing.T, wait time.Duration) {
	t.Helper()
	savedOut, savedRoster, savedFloor := outLockWait, rosterLockWait, floorLockWait
	outLockWait, rosterLockWait, floorLockWait = wait, wait, wait
	t.Cleanup(func() { outLockWait, rosterLockWait, floorLockWait = savedOut, savedRoster, savedFloor })
}

// commandWithin runs run and fails the test when it has not returned within five
// seconds.
func commandWithin(t *testing.T, run func() (int, string)) (int, string) {
	t.Helper()
	type exit struct {
		code   int
		stderr string
	}
	done := make(chan exit, 1)
	go func() {
		code, stderr := run()
		done <- exit{code, stderr}
	}()
	select {
	case got := <-done:
		return got.code, got.stderr
	case <-time.After(5 * time.Second):
		t.Fatal("the command is still running after 5s while a reader holds a lock of its output")
		return 0, ""
	}
}

// TestACommandGivesUpOnAReaderLockOfItsOutputWhenItsWaitEnds: a reader
// holding a shared flock of the output delays each command that replaces it
// only until the command's wait for a lock ends; the command then fails
// naming the output and the lock, and the output keeps its bytes. The
// control is the command once the reader let go.
func TestACommandGivesUpOnAReaderLockOfItsOutputWhenItsWaitEnds(t *testing.T) {
	for name, setup := range outWriters {
		t.Run(name, func(t *testing.T) {
			shortLockWaits(t, 300*time.Millisecond)
			out, run := setup(t)
			before := readFile(t, out)
			release := readerLocked(t, out)
			code, stderr := commandWithin(t, run)
			if code != exitFail || !strings.Contains(stderr, "--out "+out) || !strings.Contains(stderr, string(ondisk.ErrLockHeld)) {
				t.Errorf("a run under a reader's lock of --out: exit %d, stderr %q; want a refusal naming --out and the lock", code, stderr)
			}
			if got := readFile(t, out); got != before {
				t.Errorf("--out changed under a reader's lock; it holds %q", got)
			}
			release()
			if code, stderr := run(); code != exitOK {
				t.Errorf("the control, the reader gone: exit %d, stderr %q", code, stderr)
			}
		})
	}
}

// TestSuperviseViewGivesUpOnAReaderLockOfThePageWhenItsWaitEnds: a reader
// holding a shared flock of the page delays --view only until the wait for a
// lock ends; the run then fails naming --view and the lock, and the page
// keeps its bytes. The control is the run once the reader let go.
func TestSuperviseViewGivesUpOnAReaderLockOfThePageWhenItsWaitEnds(t *testing.T) {
	shortLockWaits(t, 300*time.Millisecond)
	tr := newSupTree(t)
	x := tr.export(t, tr.run, supBase, conformingCalls()...)
	tr.closeRun(t, tr.run)
	view := filepath.Join(tr.dir, "run.html")
	if code, _, stderr := tr.superviseInto(t, "first", "--evidence", x, "--view", view); code != exitOK {
		t.Fatalf("the first page: exit %d, stderr %q", code, stderr)
	}
	before := readFile(t, view)
	release := readerLocked(t, view)
	code, stderr := commandWithin(t, func() (int, string) {
		code, _, stderr := tr.superviseInto(t, "locked", "--evidence", x, "--view", view)
		return code, stderr
	})
	if code == exitOK || !strings.Contains(stderr, "--view "+view) || !strings.Contains(stderr, string(ondisk.ErrLockHeld)) {
		t.Errorf("a run under a reader's lock of the page: exit %d, stderr %q; want a failure naming --view and the lock", code, stderr)
	}
	if got := readFile(t, view); got != before {
		t.Errorf("the page changed under a reader's lock")
	}
	release()
	if code, _, stderr := tr.superviseInto(t, "control", "--evidence", x, "--view", view); code != exitOK {
		t.Errorf("the control, the reader gone: exit %d, stderr %q", code, stderr)
	}
}
