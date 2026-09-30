//go:build unix

package main

import (
	"strings"
	"syscall"
	"testing"
)

// TestAKilledSiblingOnMacOSNamesTheQuarantine: only a SIGKILL on darwin gets
// the hint, and it names the directory as one shell word.
func TestAKilledSiblingOnMacOSNamesTheQuarantine(t *testing.T) {
	t.Parallel()
	hint := killedHint("darwin", syscall.SIGKILL, "/Users/a b/demo/bin")
	for _, want := range []string{"macOS", "quarantine", "verif", "xattr -dr com.apple.quarantine '/Users/a b/demo/bin'"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the hint %q lacks %q", hint, want)
		}
	}
	for _, c := range []struct {
		goos string
		sig  syscall.Signal
	}{{"linux", syscall.SIGKILL}, {"darwin", syscall.SIGTERM}, {"darwin", syscall.SIGSEGV}} {
		if got := killedHint(c.goos, c.sig, "/x"); got != "" {
			t.Errorf("killedHint(%s, %v) = %q, want none", c.goos, c.sig, got)
		}
	}
}

// TestTheSiblingRefusalNamesTheArchives: a release user has no Go, so the
