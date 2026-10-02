//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package main

import (
	"errors"
	"os"
)

// lockExclusive refuses where no file lock is read here, so two runs never
// share a state directory unseen.
func lockExclusive(*os.File) error { return errors.New("no file lock on this platform") }
