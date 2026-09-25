package files_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/files"
)

func writeFile(t *testing.T, path string, body []byte, perm fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

// entries lists a directory's names, so a test sees a temporary file that was
// left behind.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestCreateNoReplaceWritesTheModeAsked: the temporary file is opened 0600, so
// a 0644 result proves the mode was set on the descriptor, and 0600 for the
// other file proves it is not widened either.
func TestCreateNoReplaceWritesTheModeAsked(t *testing.T) {
	dir := t.TempDir()
	for name, perm := range map[string]fs.FileMode{"public": 0o644, "private": 0o600, "read-only": 0o400} {
		if err := files.CreateNoReplace(dir, name, []byte(name+" body"), perm); err != nil {
			t.Fatalf("CreateNoReplace(%s): %v", name, err)
		}
		if got := mode(t, filepath.Join(dir, name)); got != perm {
			t.Errorf("%s: mode %04o, want %04o", name, got, perm)
		}
		if got, _ := os.ReadFile(filepath.Clean(filepath.Join(dir, name))); string(got) != name+" body" {
			t.Errorf("%s: body %q", name, got)
		}
	}
	if got := strings.Join(entries(t, dir), ","); got != "private,public,read-only" {
		t.Errorf("the directory holds %s, want the three names and nothing else", got)
	}
}

// TestCreateNoReplaceNeverReplaces: a taken name is refused as ErrExists, the
// file under it keeps its bytes and its mode, and no temporary file is left.
func TestCreateNoReplaceNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "signing.pub")
	writeFile(t, target, []byte("first"), 0o600)
	err := files.CreateNoReplace(dir, "signing.pub", []byte("second"), 0o644)
	if !errors.Is(err, files.ErrExists) {
		t.Fatalf("CreateNoReplace over a taken name = %v, want ErrExists", err)
	}
	if got, _ := os.ReadFile(filepath.Clean(target)); string(got) != "first" || mode(t, target) != 0o600 {
		t.Errorf("the taken name now holds %q with mode %04o", got, mode(t, target))
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Errorf("the directory holds %v, want the one name", got)
	}
}

// TestCreateNoReplaceRefusesADanglingLink: link(2) does not follow a symbolic
// link at the name, so a link that points nowhere is a taken name too, and
// nothing is written where it points.
func TestCreateNoReplaceRefusesADanglingLink(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(elsewhere, filepath.Join(dir, "name")); err != nil {
		t.Fatalf("linking: %v", err)
	}
	if err := files.CreateNoReplace(dir, "name", []byte("x"), 0o600); !errors.Is(err, files.ErrExists) {
		t.Fatalf("CreateNoReplace over a dangling link = %v, want ErrExists", err)
	}
	if _, err := os.Lstat(elsewhere); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link's target was created: %v", err)
	}
}

func TestReplaceReplacesWholeWithTheModeAsked(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "policy.bundle")
	writeFile(t, target, []byte("an older and longer body"), 0o600)
	if err := files.Replace(dir, "policy.bundle", []byte("new"), 0o644); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Clean(target)); string(got) != "new" {
		t.Errorf("body %q, want %q", got, "new")
	}
	if got := mode(t, target); got != 0o644 {
		t.Errorf("mode %04o, want 0644", got)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Errorf("the directory holds %v, want the one name", got)
	}
}

// TestReplaceReplacesALinkNotItsTarget: the name is renamed over, so a
// symbolic link there is replaced and the file it pointed at keeps its bytes.
func TestReplaceReplacesALinkNotItsTarget(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, outside, []byte("not yours"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Fatalf("linking: %v", err)
	}
	if err := files.Replace(dir, "out", []byte("new"), 0o644); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Clean(outside)); string(got) != "not yours" {
		t.Errorf("the link's target now holds %q", got)
	}
	if info, err := os.Lstat(filepath.Join(dir, "out")); err != nil || !info.Mode().IsRegular() {
		t.Errorf("the name is not a regular file after Replace: %v", err)
	}
}

func TestWritesIntoAMissingDirectoryFail(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := files.CreateNoReplace(missing, "x", nil, 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CreateNoReplace into a missing directory = %v", err)
	}
	if err := files.Replace(missing, "x", nil, 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Replace into a missing directory = %v", err)
	}
	if err := files.SyncDir(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SyncDir of a missing directory = %v", err)
	}
}

// TestReadRegularBound: a file of exactly the bound is read whole and one byte
// more is refused.
func TestReadRegularBound(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "at")
	over := filepath.Join(dir, "over")
	writeFile(t, at, bytes.Repeat([]byte{'a'}, 16), 0o600)
	writeFile(t, over, bytes.Repeat([]byte{'a'}, 17), 0o600)
	if raw, err := files.ReadRegular(at, 16, 0); err != nil || !bytes.Equal(raw, bytes.Repeat([]byte{'a'}, 16)) {
		t.Errorf("ReadRegular(16 bytes, limit 16) = %q, %v", raw, err)
	}
	if _, err := files.ReadRegular(over, 16, 0); !errors.Is(err, files.ErrTooLarge) {
		t.Errorf("ReadRegular(17 bytes, limit 16) = %v, want ErrTooLarge", err)
	}
}

// TestReadRegularModeTable: every permission bit in the forbidden mask
// refuses on its own, and the bits outside it never do.
func TestReadRegularModeTable(t *testing.T) {
	dir := t.TempDir()
	for perm, refused := range map[fs.FileMode]bool{
		0o400: false, 0o600: false, 0o700: false,
		0o640: true, 0o604: true, 0o620: true, 0o602: true, 0o601: true, 0o610: true,
	} {
		path := filepath.Join(dir, "k")
		writeFile(t, path, []byte("body"), perm)
		_, err := files.ReadRegular(path, 16, 0o077)
		if refused != errors.Is(err, files.ErrMode) || (!refused && err != nil) {
			t.Errorf("mode %04o under mask 0077: %v, refused want %v", perm, err, refused)
		}
		if _, err := files.ReadRegular(path, 16, 0); err != nil {
			t.Errorf("mode %04o under no mask: %v", perm, err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadRegularRefusesADirectory(t *testing.T) {
	if _, err := files.ReadRegular(t.TempDir(), 16, 0); !errors.Is(err, files.ErrNotRegular) {
		t.Errorf("ReadRegular(a directory) = %v, want ErrNotRegular", err)
	}
}

func TestCheckDir(t *testing.T) {
	root := t.TempDir()
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o770); err != nil { //nolint:gosec // G302: the group-writable directory under test
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	writeFile(t, file, nil, 0o600)
	for name, c := range map[string]struct {
		path      string
		forbidden fs.FileMode
		want      error
	}{
		"a directory":                     {root, 0o022, nil},
		"group-writable, nothing forbade": {open, 0, nil},
		"group-writable, forbidden":       {open, 0o022, files.ErrMode},
		"a file":                          {file, 0, files.ErrNotDirectory},
		"missing":                         {filepath.Join(root, "none"), 0, fs.ErrNotExist},
	} {
		err := files.CheckDir(c.path, c.forbidden)
		if (c.want == nil) != (err == nil) || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: CheckDir = %v, want %v", name, err, c.want)
		}
	}
}
