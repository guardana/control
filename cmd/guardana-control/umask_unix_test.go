//go:build unix

package main

import "syscall"

// Fixtures take their directories from t.TempDir, whose mode follows the
// process umask, and the code under test refuses a directory its group may
// write. Fixing the umask keeps a developer's own, such as 002, from failing
// tests that are not about it.
func init() {
	syscall.Umask(0o022)
}
