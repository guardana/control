//go:build unix

package gatewayconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestALinkDoesNotJoinTheStopsDirectoryToALockedOne: a stops directory that
// is a link into the spool, and a spool that is a link into the stops
// directory, each sit inside the other under a path that does not say so,
// and each is refused.
func TestALinkDoesNotJoinTheStopsDirectoryToALockedOne(t *testing.T) {
	t.Run("the stops directory links into the spool", func(t *testing.T) {
		path := write(t, withRoute)
		dir := filepath.Dir(path)
		mkdir(t, filepath.Join(dir, "spool", "inner"))
		link(t, filepath.Join(dir, "spool", "inner"), filepath.Join(dir, "stopslink"))
		setEnv(t, "reaction.stops", "stopslink")
		refusedNaming(t, path, "evidence.dir")
	})
	t.Run("the spool links into the stops directory", func(t *testing.T) {
		path := write(t, withRoute)
		dir := filepath.Dir(path)
		mkdir(t, filepath.Join(dir, "stops", "inner"))
		link(t, filepath.Join(dir, "stops", "inner"), filepath.Join(dir, "spoollink"))
		setEnv(t, "evidence.dir", "spoollink")
		refusedNaming(t, path, "evidence.dir")
	})
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

func refusedNaming(t *testing.T, path, key string) {
	t.Helper()
	_, err := Load(path, os.Environ())
	if err == nil {
		t.Fatalf("a stops directory joined to %s by a link was accepted", key)
	}
	if !strings.Contains(err.Error(), "reaction.stops") || !strings.Contains(err.Error(), key) {
		t.Errorf("the refusal names neither reaction.stops nor %s: %v", key, err)
	}
}
