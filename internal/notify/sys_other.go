//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package notify

import (
	"os"
	"os/exec"
)

// supported is false here: without a file lock, permission bits and process
// groups the state cannot be held or judged, and a timeout could leave a
// program's children running, so Run refuses.
const supported = false

const openFlags = 0

func lock(*os.File) error { return ErrUnsupported }

func ownGroup(*exec.Cmd) {}

func endGroup(*exec.Cmd) error { return ErrUnsupported }
