//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/guardana/control/internal/files"
)

// staleHealthDial bounds the dial that tells a stale socket from a live one.
const staleHealthDial = time.Second

// listenHealthSocket binds a socket at path that only this account may
// open, with mode 0600. A socket of this account that refuses connections is
// what a plane that did not stop leaves, and is replaced; anything else at
// the path is refused and left as it is.
func listenHealthSocket(path string) (net.Listener, error) {
	if err := clearStaleHealthSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("health.socket: %w", err)
	}
	ln.SetUnlinkOnClose(false)
	// Until the chmod the socket has the umask's mode; the peer check still
	// answers no other account meanwhile.
	err = os.Chmod(path, 0o600) //nolint:gosec // G703: the socket path the operator configured
	var info fs.FileInfo
	if err == nil {
		info, err = os.Lstat(path) //nolint:gosec // G703: the socket path the operator configured
	}
	if err == nil && (info.Mode().Type() != fs.ModeSocket || info.Mode().Perm() != 0o600) {
		err = fmt.Errorf("the bound socket has mode %v", info.Mode())
	}
	if err == nil {
		err = files.CheckOwnedBy(info, os.Geteuid())
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("health.socket: %w", err), ln.Close())
	}
	return &ownedSocket{UnixListener: ln, path: path, bound: info}, nil
}

// clearStaleHealthSocket removes a socket of this account at path that
// refuses a connection, and refuses anything else that is there.
func clearStaleHealthSocket(path string) error {
	info, err := os.Lstat(path) //nolint:gosec // G703: the socket path the operator configured
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("health.socket: %w", err)
	case info.Mode().Type() != fs.ModeSocket || files.CheckOwnedBy(info, os.Geteuid()) != nil:
		return fmt.Errorf("health.socket: %s is there and is not a socket of this account; nothing is bound over it", path)
	}
	conn, err := (&net.Dialer{Timeout: staleHealthDial}).DialContext(context.Background(), "unix", path)
	if err == nil {
		return errors.Join(fmt.Errorf("health.socket: something answers on %s", path), conn.Close())
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("health.socket: %s is there and may be live: %w", path, err)
	}
	if err := os.Remove(path); err != nil { //nolint:gosec // G703: the socket path the operator configured
		return fmt.Errorf("health.socket: %w", err)
	}
	return nil
}

// ownedSocket removes its file when it closes, and only while the file is
// still the socket it bound.
type ownedSocket struct {
	*net.UnixListener
	path   string
	bound  fs.FileInfo
	once   sync.Once
	closed error
}

func (s *ownedSocket) Close() error {
	s.once.Do(func() {
		s.closed = s.UnixListener.Close()
		if now, err := os.Lstat(s.path); err == nil && os.SameFile(now, s.bound) {
			s.closed = errors.Join(s.closed, os.Remove(s.path))
		}
	})
	return s.closed
}
