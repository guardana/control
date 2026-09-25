//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package holdjournal

// Without a file lock there is no way to keep two planes out of one directory,
// and two planes writing one journal would overwrite each other's holds. The
// journal refuses to open rather than carry a second locking path that no test
// in this repository builds.

func lockDir(string) (func() error, error) { return nil, ErrNoLock }
