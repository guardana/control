//go:build unix

package main

import (
	"path/filepath"
	"testing"
)

// specialOuts are the --out paths only this platform makes: a named pipe.
func specialOuts(t *testing.T, dir string) map[string]string {
	t.Helper()
	return map[string]string{"a named pipe": mkfifo(t, filepath.Join(dir, "statement-pipe"))}
}
