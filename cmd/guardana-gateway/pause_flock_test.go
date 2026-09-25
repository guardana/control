//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDoctorTakesNoLockAndWritesNothingBesideThePauseFile: the pause file's
// directory belongs to its writers, who lock it. doctor, which builds the
// plane and reads the file, must neither wait for that lock nor take it, and
// must leave the directory and the file as it found them.
func TestDoctorTakesNoLockAndWritesNothingBesideThePauseFile(t *testing.T) {
	tr := newTree(t)
	path := tr.withPauseFile(t, pauseDocument(pauseEntry("orders", scopeOrders)), time.Second)
	before := pauseDirState(t, path)
	holdWritersLock(t, filepath.Dir(path))

	done := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		doctor(context.Background(), tr.config, &out, &out)
		done <- out.String()
	}()
	select {
	case out := <-done:
		for _, check := range []string{"ok      pause", "ok      seams"} {
			if !strings.Contains(out, check) {
				t.Errorf("%q is missing while a writer holds the pause directory's lock:\n%s", check, out)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("doctor did not finish: it waited for the lock a writer of the pause file holds")
	}
	if after := pauseDirState(t, path); after != before {
		t.Errorf("the pause directory changed across a doctor run:\nbefore %+v\nafter  %+v", before, after)
	}
}

// dirState is what a run could change beside and in the pause file: the
// names in its directory, and the file's bytes, time and mode.
type dirState struct {
	names, body string
	modified    int64
	mode        os.FileMode
}

func pauseDirState(t *testing.T, path string) dirState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return dirState{
		names: strings.Join(namesUnder(t, filepath.Dir(path)), "\n"), body: readFile(t, path),
		modified: info.ModTime().UnixNano(), mode: info.Mode(),
	}
}

// holdWritersLock takes the lock a writer of the pause file takes, for the
// rest of the test, as another process would.
func holdWritersLock(t *testing.T, dir string) {
	t.Helper()
	held, err := os.Open(dir) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("taking the writers' lock: %v", err)
	}
}

// TestABundleThatIsANamedPipeIsRefusedAtOnce: a plane reads its bundle with
// an open that does not wait, so a named pipe configured in its place is a
// refusal and not a start that never ends.
func TestABundleThatIsANamedPipeIsRefusedAtOnce(t *testing.T) {
	tr := newTree(t)
	pipe := filepath.Join(tr.dir, "pipe.bundle")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("making the named pipe: %v", err)
	}
	setEnv(t, "policy.bundle_file", pipe)
	done := make(chan string, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		serve(context.Background(), tr.config, &stdout, &stderr)
		done <- stderr.String()
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, "policy.bundle_file") || !strings.Contains(got, "not a regular file") {
			t.Errorf("the refusal %q does not name the key and the pipe", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run waited on a named pipe configured as the bundle")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
