//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package enforce_test

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/guardana/control/adapters/enforce"
)

// holdSocketLock takes the lock a live receiver holds beside socket, as
// another process would, and returns what releases it.
func holdSocketLock(t *testing.T, socket string) func() {
	t.Helper()
	f, err := os.OpenFile(socket+".lock", os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return func() { _ = f.Close() }
}

// staleSocket leaves a socket at path that refuses every connection.
func staleSocket(t *testing.T, path string) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ul, ok := ln.(*net.UnixListener)
	if !ok {
		t.Fatal("not a unix listener")
	}
	ul.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestALiveReceiversLockKeepsItsSocket: a refused connection is no proof on
// macOS, where a live receiver whose backlog is full refuses too, so while
// the lock beside the socket is held the socket stays and Listen refuses.
func TestALiveReceiversLockKeepsItsSocket(t *testing.T) {
	socket := socketPath(t)
	staleSocket(t, socket)
	release := holdSocketLock(t, socket)
	if err := listenAt(t, socket); !errors.Is(err, enforce.ErrSocketInUse) {
		t.Fatalf("Listen while the lock is held = %v, want ErrSocketInUse", err)
	}
	if _, err := os.Lstat(socket); err != nil {
		t.Fatalf("the socket of a receiver holding its lock was removed: %v", err)
	}
	release()
	if err := listenAt(t, socket); err != nil {
		t.Fatalf("Listen once the lock is free: %v", err)
	}
}

// TestAListenerHoldsItsLockUntilClosed: a second receiver on one socket is
// refused for as long as the first's listener is open, and binds once it is
// closed.
func TestAListenerHoldsItsLockUntilClosed(t *testing.T) {
	socket := socketPath(t)
	cfg := validConfig()
	cfg.Socket = socket
	a, err := enforce.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Listen()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(socket + ".lock") //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("taking the lock of an open listener = %v, want EWOULDBLOCK", err)
	}
	if err := listenAt(t, socket); !errors.Is(err, enforce.ErrSocketInUse) {
		t.Fatalf("a second Listen = %v, want ErrSocketInUse", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listenAt(t, socket); err != nil {
		t.Fatalf("Listen after the first listener closed: %v", err)
	}
}

// TestALockPathThatIsNotThePlanesIsRefused: a link, a directory, or a file
// others may read or write at the lock's path is refused, and the socket's
// path is left as it was.
func TestALockPathThatIsNotThePlanesIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, lock string)
	}{
		{"a link", func(t *testing.T, lock string) {
			if err := os.Symlink(lock+".target", lock); err != nil {
				t.Fatal(err)
			}
		}},
		{"a directory", func(t *testing.T, lock string) {
			if err := os.Mkdir(lock, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file others may read", func(t *testing.T, lock string) {
			if err := os.WriteFile(lock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(lock, 0o644); err != nil { //nolint:gosec // G302: the mode under test
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := socketPath(t)
			tc.prepare(t, socket+".lock")
			if err := listenAt(t, socket); !errors.Is(err, enforce.ErrSocketInUse) {
				t.Fatalf("Listen = %v, want ErrSocketInUse", err)
			}
			if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a socket was bound beside a refused lock: %v", err)
			}
		})
	}
}
