//go:build linux || darwin

package files

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openJudged opens the entry at name in the directory root holds for
// reading, without following a link or waiting on a pipe; a link at the
// name is errLink.
func openJudged(root *os.Root, name string) (*os.File, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	closed := d.Close()
	switch {
	case errors.Is(err, unix.ELOOP):
		return nil, errors.Join(errLink, closed)
	case err != nil:
		return nil, errors.Join(&os.PathError{Op: "openat", Path: name, Err: err}, closed)
	}
	f := os.NewFile(uintptr(fd), name)
	if closed != nil {
		return nil, errors.Join(closed, f.Close())
	}
	return f, nil
}

// lockFile takes an exclusive flock of f, waiting while another open of the
// file holds one; closing f lets it go.
func lockFile(f *os.File) error {
	for {
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
